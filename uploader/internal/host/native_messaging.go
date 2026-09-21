package host

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

const MaxFrameBytes = protocol.MaxFrameBytes

var (
	ErrFrameTooLarge       = errors.New("native messaging frame exceeds application cap")
	ErrMalformedFrame      = errors.New("malformed native messaging frame")
	ErrOriginNotConfigured = errors.New("native messaging allowed origin is not configured")
	ErrOriginMismatch      = errors.New("native messaging origin is not allowed")
	ErrInvalidHandler      = errors.New("native messaging request handler is not configured")
)

type FrameError struct {
	Kind          error
	ClaimedLength uint32
	Cause         error
}

func (e *FrameError) Error() string {
	if e == nil {
		return "native messaging frame error"
	}
	if e.Cause != nil {
		return e.Kind.Error() + ": " + e.Cause.Error()
	}
	return e.Kind.Error()
}

func (e *FrameError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

// ReadFrame reads one Chrome Native Messaging payload.  The length is a
// little-endian uint32 and is checked before allocating or reading the body.
// A claimed frame over one MiB is intentionally fatal to this stdio session:
// the body cannot be safely resynchronized without trusting an unbounded
// attacker-controlled length.
func ReadFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	read, err := io.ReadFull(reader, header[:])
	if err != nil {
		if err == io.EOF && read == 0 {
			return nil, io.EOF
		}
		return nil, &FrameError{Kind: ErrMalformedFrame, Cause: io.ErrUnexpectedEOF}
	}

	claimedLength := binary.LittleEndian.Uint32(header[:])
	if uint64(claimedLength) > uint64(MaxFrameBytes) {
		return nil, &FrameError{Kind: ErrFrameTooLarge, ClaimedLength: claimedLength}
	}

	payload := make([]byte, int(claimedLength))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, &FrameError{Kind: ErrMalformedFrame, ClaimedLength: claimedLength, Cause: io.ErrUnexpectedEOF}
	}
	return payload, nil
}

// WriteFrame writes one complete framed Native Messaging payload.  It rejects
// oversized application messages in either direction and never truncates or
// chunks a logical protocol message.
func WriteFrame(writer io.Writer, payload []byte) error {
	if len(payload) > MaxFrameBytes {
		return &FrameError{Kind: ErrFrameTooLarge, ClaimedLength: uint32(len(payload))}
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

// RequestHandler is implemented by the later queue/auth processor.  Keeping
// it at the host boundary means framing and protocol validation do not need to
// know about SQLite, Keychain, GitHub, or logging.
type RequestHandler interface {
	HandleRequest(context.Context, protocol.Request) (any, error)
}

// SessionLifecycle lets the queue processor open/resume its single drain on a
// connect request and release process-local resources when Chrome closes the
// persistent port.  It deliberately has no queue or credential methods.
type SessionLifecycle interface {
	OnConnect(context.Context) error
	OnDisconnect()
}

type Server struct {
	Reader        io.Reader
	Writer        io.Writer
	Handler       RequestHandler
	Lifecycle     SessionLifecycle
	Origin        string
	AllowedOrigin string
	writeMu       sync.Mutex
}

func NewServer(reader io.Reader, writer io.Writer, handler RequestHandler, lifecycle SessionLifecycle, origin, allowedOrigin string) *Server {
	return &Server{
		Reader:        reader,
		Writer:        writer,
		Handler:       handler,
		Lifecycle:     lifecycle,
		Origin:        origin,
		AllowedOrigin: allowedOrigin,
	}
}

// Run owns one persistent Native Messaging connection.  It validates the
// Chrome origin before reading, keeps the process alive until stdin closes,
// serializes request handling on the stdio loop, and emits only closed,
// request-correlated protocol responses.
func (s *Server) Run(ctx context.Context) error {
	if err := ValidateOrigin(s.Origin, s.AllowedOrigin); err != nil {
		return err
	}
	if s.Reader == nil || s.Writer == nil || s.Handler == nil {
		return ErrInvalidHandler
	}
	if s.Lifecycle != nil {
		defer s.Lifecycle.OnDisconnect()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		payload, err := ReadFrame(s.Reader)
		if err != nil {
			if errors.Is(err, ErrFrameTooLarge) {
				// No requestId can be trusted because the oversized body was not
				// parsed.  Send one bounded diagnostic frame, then terminate the
				// session rather than attempting to resynchronize.
				_ = s.writeError(nil, protocol.ErrorRejectedOversized, false)
			}
			return err
		}

		request, requestErr := protocol.DecodeRequest(payload)
		if requestErr != nil {
			if err := s.writeValidationFailure(request, requestErr); err != nil {
				return err
			}
			continue
		}

		if request.Type == protocol.RequestConnect && s.Lifecycle != nil {
			if err := s.Lifecycle.OnConnect(ctx); err != nil {
				category, retryable := safeError(err)
				if writeErr := s.writeError(requestIDPointer(request), category, retryable); writeErr != nil {
					return writeErr
				}
				continue
			}
		}

		response, handlerErr := s.Handler.HandleRequest(ctx, request)
		if handlerErr != nil {
			category, retryable := safeError(handlerErr)
			if err := s.writeError(requestIDPointer(request), category, retryable); err != nil {
				return err
			}
			continue
		}

		encoded, encodeErr := protocol.EncodeResponse(response)
		if encodeErr != nil {
			if err := s.writeError(requestIDPointer(request), protocol.ErrorInternal, false); err != nil {
				return err
			}
			continue
		}
		if err := s.writePayload(encoded); err != nil {
			return err
		}
	}
}

func (s *Server) writeValidationFailure(request protocol.Request, err error) error {
	category, _ := safeError(err)
	if request.Type == protocol.RequestSubmitJob && isSubmitRejection(category) {
		ack := rejectedAck(request, protocol.AckStatus(category))
		encoded, encodeErr := protocol.EncodeResponse(ack)
		if encodeErr != nil {
			return encodeErr
		}
		return s.writePayload(encoded)
	}
	return s.writeError(requestIDPointer(request), category, false)
}

func (s *Server) writeError(requestID *string, category protocol.ErrorCategory, retryable bool) error {
	message := protocol.ErrorMessage{
		Type:            "error",
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       requestID,
		Category:        category,
		Retryable:       retryable,
	}
	encoded, err := protocol.EncodeResponse(message)
	if err != nil {
		return err
	}
	return s.writePayload(encoded)
}

func (s *Server) writePayload(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return WriteFrame(s.Writer, payload)
}

func rejectedAck(request protocol.Request, status protocol.AckStatus) protocol.Ack {
	ack := protocol.Ack{
		Type:            "ack",
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       request.RequestID,
		Operation:       "submit",
		Status:          status,
	}
	if request.Job == nil {
		return ack
	}
	if protocol.IsValidLectureKey(request.Job.LectureKey) {
		value := request.Job.LectureKey
		ack.LectureKey = &value
	}
	if protocol.IsValidContentHash(request.Job.ContentHash) {
		value := request.Job.ContentHash
		ack.ContentHash = &value
	}
	return ack
}

func isSubmitRejection(category protocol.ErrorCategory) bool {
	switch category {
	case protocol.ErrorRejectedInvalidSchema, protocol.ErrorRejectedUnknownField,
		protocol.ErrorRejectedOversized, protocol.ErrorRejectedInvalidHash,
		protocol.ErrorRejectedUnsafeURL, protocol.ErrorRejectedQueueFull:
		return true
	default:
		return false
	}
}

func requestIDPointer(request protocol.Request) *string {
	if request.RequestID == "" {
		return nil
	}
	value := request.RequestID
	return &value
}

func safeError(err error) (protocol.ErrorCategory, bool) {
	var protocolErr protocol.ProtocolError
	if errors.As(err, &protocolErr) && protocol.IsErrorCategory(protocolErr.Category) {
		return protocolErr.Category, protocolErr.Retryable
	}
	return protocol.ErrorInternal, false
}

var extensionIDPattern = regexp.MustCompile(`^[a-p]{32}$`)

// ExtensionOrigin formats the only origin shape accepted by the host.  It is
// kept separate from installation so the installer can pass the actual loaded
// extension ID without this package guessing one.
func ExtensionOrigin(extensionID string) (string, error) {
	if !extensionIDPattern.MatchString(extensionID) {
		return "", ErrOriginMismatch
	}
	return "chrome-extension://" + extensionID + "/", nil
}

// ValidateOrigin is an exact allowlist check.  Wildcards, missing expected
// origins, alternate schemes, and path variations all fail closed.
func ValidateOrigin(origin, allowedOrigin string) error {
	if allowedOrigin == "" {
		return ErrOriginNotConfigured
	}
	if _, err := validateOriginShape(allowedOrigin); err != nil {
		return err
	}
	if _, err := validateOriginShape(origin); err != nil {
		return err
	}
	if origin != allowedOrigin {
		return ErrOriginMismatch
	}
	return nil
}

func validateOriginShape(origin string) (string, error) {
	if origin == "" || origin == "*" {
		return "", ErrOriginMismatch
	}
	const prefix = "chrome-extension://"
	if len(origin) <= len(prefix)+len("/") || origin[:len(prefix)] != prefix || origin[len(origin)-1] != '/' {
		return "", ErrOriginMismatch
	}
	if !extensionIDPattern.MatchString(origin[len(prefix) : len(origin)-1]) {
		return "", ErrOriginMismatch
	}
	return origin, nil
}

// FormatOriginArgument returns the first Chrome-supplied native-host argument.
// Chrome supplies the extension origin as argv[1]; callers should still pass
// the result through ValidateOrigin with the installer-configured allowlist.
func FormatOriginArgument(args []string) (string, error) {
	if len(args) == 0 || args[0] == "" {
		return "", fmt.Errorf("missing native messaging origin")
	}
	return args[0], nil
}
