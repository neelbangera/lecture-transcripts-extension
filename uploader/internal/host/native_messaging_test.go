package host

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

const testExtensionID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testOrigin() string {
	origin, err := ExtensionOrigin(testExtensionID)
	if err != nil {
		panic(err)
	}
	return origin
}

func frame(payload []byte) []byte {
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(payload)))
	return append(header, payload...)
}

func decodeAllFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var frames [][]byte
	reader := bytes.NewReader(data)
	for {
		payload, err := ReadFrame(reader)
		if errors.Is(err, io.EOF) {
			return frames
		}
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		frames = append(frames, payload)
	}
}

type chunkReader struct {
	data []byte
	step int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	size := r.step
	if size > len(r.data) {
		size = len(r.data)
	}
	if size > len(p) {
		size = len(p)
	}
	copy(p, r.data[:size])
	r.data = r.data[size:]
	return size, nil
}

type chunkWriter struct {
	buffer bytes.Buffer
	step   int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	size := w.step
	if size > len(p) {
		size = len(p)
	}
	return w.buffer.Write(p[:size])
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestReadFrameLittleEndianLength(t *testing.T) {
	payload := []byte("hello")
	got, err := ReadFrame(bytes.NewReader([]byte{0x05, 0x00, 0x00, 0x00, 'h', 'e', 'l', 'l', 'o'}))
	if err != nil || string(got) != string(payload) {
		t.Fatalf("ReadFrame = %q, %v; want %q", got, err, payload)
	}

	long := bytes.Repeat([]byte("x"), 300)
	got, err = ReadFrame(bytes.NewReader(frame(long)))
	if err != nil || !bytes.Equal(got, long) {
		t.Fatalf("ReadFrame(300 bytes) failed: len=%d err=%v", len(got), err)
	}
}

func TestReadFrameShortReads(t *testing.T) {
	payload := []byte(`{"type":"reset","protocolVersion":1,"requestId":"req-1"}`)
	for _, step := range []int{1, 3} {
		t.Run(fmt.Sprintf("step-%d", step), func(t *testing.T) {
			reader := &chunkReader{data: frame(payload), step: step}
			got, err := ReadFrame(reader)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("ReadFrame = %q, %v; want %q", got, err, payload)
			}
		})
	}
}

func TestReadFrameCleanEOF(t *testing.T) {
	if _, err := ReadFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Fatalf("empty reader error = %v, want io.EOF", err)
	}

	reader := bytes.NewReader(frame([]byte("one")))
	if _, err := ReadFrame(reader); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if _, err := ReadFrame(reader); !errors.Is(err, io.EOF) {
		t.Fatalf("second frame error = %v, want io.EOF", err)
	}
}

func TestReadFrameTruncated(t *testing.T) {
	for length := 1; length <= 3; length++ {
		reader := bytes.NewReader([]byte{0x05, 0x00, 0x00}[0:length])
		_, err := ReadFrame(reader)
		if !errors.Is(err, ErrMalformedFrame) {
			t.Fatalf("truncated header length %d error = %v, want ErrMalformedFrame", length, err)
		}
	}

	_, err := ReadFrame(bytes.NewReader([]byte{0x0a, 0x00, 0x00, 0x00, 'a', 'b', 'c'}))
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("truncated body error = %v, want ErrMalformedFrame", err)
	}
	var frameErr *FrameError
	if !errors.As(err, &frameErr) || frameErr.ClaimedLength != 10 {
		t.Fatalf("truncated body FrameError = %#v, want claimed length 10", frameErr)
	}
}

func TestReadFrameZeroLength(t *testing.T) {
	payload, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 0}))
	if err != nil || len(payload) != 0 {
		t.Fatalf("zero-length frame = %q, %v", payload, err)
	}
}

func TestReadFrameOversizedClaimedLength(t *testing.T) {
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(MaxFrameBytes)+1)
	body := bytes.Repeat([]byte("x"), 16)
	reader := bytes.NewReader(append(header, body...))

	_, err := ReadFrame(reader)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized frame error = %v, want ErrFrameTooLarge", err)
	}
	var frameErr *FrameError
	if !errors.As(err, &frameErr) || frameErr.ClaimedLength != uint32(MaxFrameBytes)+1 {
		t.Fatalf("FrameError = %#v, want claimed length %d", frameErr, MaxFrameBytes+1)
	}
	if reader.Len() != len(body) {
		t.Fatalf("oversized claimed frame consumed body bytes: %d remain", reader.Len())
	}
}

func TestReadFrameExactLimit(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), MaxFrameBytes)
	got, err := ReadFrame(bytes.NewReader(frame(payload)))
	if err != nil || len(got) != MaxFrameBytes {
		t.Fatalf("exact limit frame failed: len=%d err=%v", len(got), err)
	}
}

func TestWriteFrameLittleEndianLength(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, []byte("hello")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	want := []byte{0x05, 0x00, 0x00, 0x00, 'h', 'e', 'l', 'l', 'o'}
	if !bytes.Equal(buffer.Bytes(), want) {
		t.Fatalf("WriteFrame = %v, want %v", buffer.Bytes(), want)
	}
}

func TestWriteFrameShortWrites(t *testing.T) {
	payload := []byte("short writes must still produce one complete frame")
	writer := &chunkWriter{step: 1}
	if err := WriteFrame(writer, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := ReadFrame(bytes.NewReader(writer.buffer.Bytes()))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("round trip = %q, %v; want %q", got, err, payload)
	}
}

func TestWriteFrameErrors(t *testing.T) {
	if err := WriteFrame(errorWriter{}, []byte("payload")); err == nil {
		t.Fatal("WriteFrame must propagate writer errors")
	}
	if err := WriteFrame(&bytes.Buffer{}, bytes.Repeat([]byte("x"), MaxFrameBytes+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized WriteFrame error = %v, want ErrFrameTooLarge", err)
	}
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, bytes.Repeat([]byte("x"), MaxFrameBytes)); err != nil {
		t.Fatalf("exact limit WriteFrame: %v", err)
	}
}

func TestExtensionOrigin(t *testing.T) {
	origin, err := ExtensionOrigin(testExtensionID)
	if err != nil || origin != "chrome-extension://"+testExtensionID+"/" {
		t.Fatalf("ExtensionOrigin = %q, %v", origin, err)
	}
	for _, invalid := range []string{"", "abc", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("A", 32), strings.Repeat("0", 32)} {
		if _, err := ExtensionOrigin(invalid); !errors.Is(err, ErrOriginMismatch) {
			t.Fatalf("ExtensionOrigin(%q) error = %v, want ErrOriginMismatch", invalid, err)
		}
	}
}

func TestValidateOrigin(t *testing.T) {
	origin := testOrigin()
	if err := ValidateOrigin(origin, origin); err != nil {
		t.Fatalf("exact origin match rejected: %v", err)
	}
	if err := ValidateOrigin(origin, ""); !errors.Is(err, ErrOriginNotConfigured) {
		t.Fatalf("missing allowlist error = %v, want ErrOriginNotConfigured", err)
	}
	if err := ValidateOrigin("*", origin); !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("wildcard origin error = %v, want ErrOriginMismatch", err)
	}
	if err := ValidateOrigin(origin, "*"); !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("wildcard allowlist error = %v, want ErrOriginMismatch", err)
	}
	other := "chrome-extension://" + strings.Repeat("b", 32) + "/"
	if err := ValidateOrigin(other, origin); !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("mismatched origin error = %v, want ErrOriginMismatch", err)
	}
	for _, malformed := range []string{
		"",
		origin + "extra",
		strings.TrimSuffix(origin, "/"),
		"https://" + testExtensionID + "/",
		"chrome-extension://" + testExtensionID,
		"chrome-extension://" + strings.Repeat("z", 32) + "/",
		"chrome-extension://" + testExtensionID + "/path",
	} {
		if err := ValidateOrigin(malformed, origin); !errors.Is(err, ErrOriginMismatch) {
			t.Fatalf("ValidateOrigin(%q) error = %v, want ErrOriginMismatch", malformed, err)
		}
	}
}

func TestFormatOriginArgument(t *testing.T) {
	if _, err := FormatOriginArgument(nil); err == nil {
		t.Fatal("missing origin argument must fail")
	}
	if _, err := FormatOriginArgument([]string{""}); err == nil {
		t.Fatal("empty origin argument must fail")
	}
	got, err := FormatOriginArgument([]string{testOrigin(), "extra"})
	if err != nil || got != testOrigin() {
		t.Fatalf("FormatOriginArgument = %q, %v", got, err)
	}
}

type recordingHandler struct {
	mu       sync.Mutex
	requests []protocol.Request
	respond  func(protocol.Request) (any, error)
}

func (h *recordingHandler) HandleRequest(_ context.Context, request protocol.Request) (any, error) {
	h.mu.Lock()
	h.requests = append(h.requests, request)
	h.mu.Unlock()
	if h.respond != nil {
		return h.respond(request)
	}
	return validStatusResponse(), nil
}

func (h *recordingHandler) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func (h *recordingHandler) requestTypes() []protocol.RequestType {
	h.mu.Lock()
	defer h.mu.Unlock()
	types := make([]protocol.RequestType, 0, len(h.requests))
	for _, request := range h.requests {
		types = append(types, request.Type)
	}
	return types
}

type recordingLifecycle struct {
	mu          sync.Mutex
	connects    int
	disconnects int
	connectErr  error
}

func (l *recordingLifecycle) OnConnect(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.connects++
	return l.connectErr
}

func (l *recordingLifecycle) OnDisconnect() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.disconnects++
}

func (l *recordingLifecycle) counts() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.connects, l.disconnects
}

func validStatusResponse() protocol.StatusMessage {
	return protocol.StatusMessage{
		Type:             "status",
		ProtocolVersion:  protocol.ProtocolVersion,
		ExtensionVersion: "0.1.0",
		UploaderVersion:  "0.2.0",
		AuthState:        protocol.AuthNotConnected,
		DrainState:       protocol.DrainIdle,
		Jobs:             []protocol.JobSummary{},
	}
}

func runServer(t *testing.T, reader io.Reader, handler RequestHandler, lifecycle SessionLifecycle) (bytes.Buffer, error) {
	t.Helper()
	var writer bytes.Buffer
	server := NewServer(reader, &writer, handler, lifecycle, testOrigin(), testOrigin())
	err := server.Run(context.Background())
	return writer, err
}

func TestServerRunProcessesFramesAndLifecycle(t *testing.T) {
	connect := []byte(`{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":"0.1.0"}`)
	status := []byte(`{"type":"status_request","protocolVersion":1,"requestId":"req-2"}`)
	handler := &recordingHandler{}
	lifecycle := &recordingLifecycle{}

	writer, err := runServer(t, bytes.NewReader(append(frame(connect), frame(status)...)), handler, lifecycle)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Run error = %v, want io.EOF", err)
	}
	if handler.callCount() != 2 {
		t.Fatalf("handler calls = %d, want 2", handler.callCount())
	}
	connects, disconnects := lifecycle.counts()
	if connects != 1 || disconnects != 1 {
		t.Fatalf("lifecycle = %d connects, %d disconnects; want 1, 1", connects, disconnects)
	}

	frames := decodeAllFrames(t, writer.Bytes())
	if len(frames) != 2 {
		t.Fatalf("response frames = %d, want 2", len(frames))
	}
	for index, payload := range frames {
		if _, err := protocol.DecodeResponse(payload); err != nil {
			t.Fatalf("response frame %d invalid: %v", index, err)
		}
	}
}

func TestServerRunInvalidRequestNeverReachesHandler(t *testing.T) {
	payload := []byte(`{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":"0.1.0","extra":true}`)
	handler := &recordingHandler{}
	writer, err := runServer(t, bytes.NewReader(frame(payload)), handler, nil)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Run error = %v, want io.EOF", err)
	}
	if handler.callCount() != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.callCount())
	}
	frames := decodeAllFrames(t, writer.Bytes())
	if len(frames) != 1 {
		t.Fatalf("response frames = %d, want 1", len(frames))
	}
	var message protocol.ErrorMessage
	if err := json.Unmarshal(frames[0], &message); err != nil {
		t.Fatalf("decode error frame: %v", err)
	}
	if message.Category != protocol.ErrorRejectedUnknownField || message.RequestID == nil || *message.RequestID != "req-1" {
		t.Fatalf("unexpected error frame: %+v", message)
	}
}

func TestServerRunSubmitRejectionReturnsAck(t *testing.T) {
	job := `{"schemaVersion":1,"lectureKey":"eecs484/2026-fall/001","courseSlug":"eecs484","courseName":"EECS 484","term":"2026-fall","lectureNumber":1,"lectureDate":"2026-09-01","sourceUrl":"https://leccap.engin.umich.edu/a","capturedAt":"2026-09-20T12:34:56Z","transcript":"` + strings.Repeat("word ", 20) + `","timestampedTranscript":"","contentHash":"` + strings.Repeat("A", 64) + `"}`
	payload := []byte(`{"type":"submit_job","protocolVersion":1,"requestId":"req-9","job":` + job + `}`)
	handler := &recordingHandler{}
	writer, err := runServer(t, bytes.NewReader(frame(payload)), handler, nil)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Run error = %v, want io.EOF", err)
	}
	if handler.callCount() != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.callCount())
	}
	frames := decodeAllFrames(t, writer.Bytes())
	if len(frames) != 1 {
		t.Fatalf("response frames = %d, want 1", len(frames))
	}
	var ack protocol.Ack
	if err := json.Unmarshal(frames[0], &ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.Type != "ack" || ack.Status != protocol.AckRejectedInvalidHash || ack.RequestID != "req-9" {
		t.Fatalf("unexpected ack: %+v", ack)
	}
	if ack.LectureKey == nil || *ack.LectureKey != "eecs484/2026-fall/001" {
		t.Fatalf("ack must echo a valid lecture key: %+v", ack)
	}
	if ack.ContentHash != nil {
		t.Fatalf("ack must not echo an invalid content hash: %+v", ack)
	}
}

func TestServerRunOversizedClaimedFrameTerminatesSession(t *testing.T) {
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(MaxFrameBytes)+1)
	valid := []byte(`{"type":"reset","protocolVersion":1,"requestId":"req-1"}`)
	stream := append(header, frame(valid)...)
	handler := &recordingHandler{}

	writer, err := runServer(t, bytes.NewReader(stream), handler, nil)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Run error = %v, want ErrFrameTooLarge", err)
	}
	if handler.callCount() != 0 {
		t.Fatalf("handler calls = %d, want 0 after oversized frame", handler.callCount())
	}
	frames := decodeAllFrames(t, writer.Bytes())
	if len(frames) != 1 {
		t.Fatalf("diagnostic frames = %d, want 1", len(frames))
	}
	var message protocol.ErrorMessage
	if err := json.Unmarshal(frames[0], &message); err != nil {
		t.Fatalf("decode diagnostic frame: %v", err)
	}
	if message.Category != protocol.ErrorRejectedOversized || message.RequestID != nil {
		t.Fatalf("unexpected diagnostic frame: %+v", message)
	}
}

func TestServerRunHandlerErrorsBecomeSafeResponses(t *testing.T) {
	payload := []byte(`{"type":"reset","protocolVersion":1,"requestId":"req-1"}`)
	cases := []struct {
		name      string
		handler   func(protocol.Request) (any, error)
		category  protocol.ErrorCategory
		retryable bool
	}{
		{
			name: "classified error",
			handler: func(protocol.Request) (any, error) {
				return nil, protocol.NewProtocolError(protocol.ErrorNotConnected, true)
			},
			category:  protocol.ErrorNotConnected,
			retryable: true,
		},
		{
			name: "wrapped classified error",
			handler: func(protocol.Request) (any, error) {
				return nil, fmt.Errorf("database exploded: %w", protocol.NewProtocolError(protocol.ErrorInvalidState, false))
			},
			category:  protocol.ErrorInvalidState,
			retryable: false,
		},
		{
			name: "unclassified error",
			handler: func(protocol.Request) (any, error) {
				return nil, errors.New("raw internal detail: SELECT * FROM secrets")
			},
			category:  protocol.ErrorInternal,
			retryable: false,
		},
		{
			name: "unencodable response",
			handler: func(protocol.Request) (any, error) {
				return struct{ Raw string }{Raw: "not a protocol message"}, nil
			},
			category:  protocol.ErrorInternal,
			retryable: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := &recordingHandler{respond: testCase.handler}
			writer, err := runServer(t, bytes.NewReader(frame(payload)), handler, nil)
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Run error = %v, want io.EOF", err)
			}
			frames := decodeAllFrames(t, writer.Bytes())
			if len(frames) != 1 {
				t.Fatalf("response frames = %d, want 1", len(frames))
			}
			var message protocol.ErrorMessage
			if err := json.Unmarshal(frames[0], &message); err != nil {
				t.Fatalf("decode error frame: %v", err)
			}
			if message.Category != testCase.category || message.Retryable != testCase.retryable {
				t.Fatalf("error frame = %+v, want %s retryable=%v", message, testCase.category, testCase.retryable)
			}
			if strings.Contains(string(frames[0]), "raw internal detail") || strings.Contains(string(frames[0]), "SELECT") {
				t.Fatalf("error frame leaked raw error text: %s", frames[0])
			}
		})
	}
}

func TestServerRunSerializesRequestsInOrder(t *testing.T) {
	handler := &recordingHandler{respond: func(request protocol.Request) (any, error) {
		response := validStatusResponse()
		if request.RequestID != "" {
			response.RequestID = &request.RequestID
		}
		return response, nil
	}}
	lifecycle := &recordingLifecycle{}
	payloads := [][]byte{
		[]byte(`{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":"0.1.0"}`),
		[]byte(`{"type":"status_request","protocolVersion":1,"requestId":"req-2"}`),
		[]byte(`{"type":"retry_job","protocolVersion":1,"requestId":"req-3","jobId":4}`),
		[]byte(`{"type":"reset","protocolVersion":1,"requestId":"req-4"}`),
	}
	var stream []byte
	for _, payload := range payloads {
		stream = append(stream, frame(payload)...)
	}
	writer, err := runServer(t, bytes.NewReader(stream), handler, lifecycle)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Run error = %v, want io.EOF", err)
	}
	wantTypes := []protocol.RequestType{
		protocol.RequestConnect, protocol.RequestStatus,
		protocol.RequestRetryJob, protocol.RequestReset,
	}
	if got := handler.requestTypes(); !equalRequestTypes(got, wantTypes) {
		t.Fatalf("handler order = %v, want %v", got, wantTypes)
	}
	frames := decodeAllFrames(t, writer.Bytes())
	if len(frames) != len(payloads) {
		t.Fatalf("response frames = %d, want %d", len(frames), len(payloads))
	}
	for index, payload := range frames {
		decoded, err := protocol.DecodeResponse(payload)
		if err != nil {
			t.Fatalf("frame %d invalid: %v", index, err)
		}
		status, ok := decoded.(protocol.StatusMessage)
		if !ok || status.RequestID == nil {
			t.Fatalf("frame %d is not a correlated status: %+v", index, decoded)
		}
		wantRequestID := fmt.Sprintf("req-%d", index+1)
		if *status.RequestID != wantRequestID {
			t.Fatalf("frame %d requestId = %q, want %q", index, *status.RequestID, wantRequestID)
		}
	}
}

func equalRequestTypes(got, want []protocol.RequestType) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestServerRunConnectLifecycleFailure(t *testing.T) {
	payload := []byte(`{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":"0.1.0"}`)
	handler := &recordingHandler{}
	lifecycle := &recordingLifecycle{connectErr: protocol.NewProtocolError(protocol.ErrorInvalidState, false)}
	writer, err := runServer(t, bytes.NewReader(frame(payload)), handler, lifecycle)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Run error = %v, want io.EOF", err)
	}
	if handler.callCount() != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.callCount())
	}
	frames := decodeAllFrames(t, writer.Bytes())
	if len(frames) != 1 {
		t.Fatalf("response frames = %d, want 1", len(frames))
	}
	var message protocol.ErrorMessage
	if err := json.Unmarshal(frames[0], &message); err != nil {
		t.Fatalf("decode error frame: %v", err)
	}
	if message.Category != protocol.ErrorInvalidState {
		t.Fatalf("error frame = %+v, want invalid_state", message)
	}
}

func TestServerRunRejectsOriginBeforeReading(t *testing.T) {
	payload := []byte(`{"type":"reset","protocolVersion":1,"requestId":"req-1"}`)
	handler := &recordingHandler{}
	var writer bytes.Buffer
	server := NewServer(bytes.NewReader(frame(payload)), &writer, handler, nil, "chrome-extension://"+strings.Repeat("b", 32)+"/", testOrigin())
	if err := server.Run(context.Background()); !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("Run error = %v, want ErrOriginMismatch", err)
	}
	if handler.callCount() != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.callCount())
	}
	if writer.Len() != 0 {
		t.Fatalf("mismatched origin must not write frames: %s", writer.Bytes())
	}
}

func TestServerRunRejectsMissingOriginConfiguration(t *testing.T) {
	handler := &recordingHandler{}
	var writer bytes.Buffer
	server := NewServer(bytes.NewReader(nil), &writer, handler, nil, testOrigin(), "")
	if err := server.Run(context.Background()); !errors.Is(err, ErrOriginNotConfigured) {
		t.Fatalf("Run error = %v, want ErrOriginNotConfigured", err)
	}
}

func TestServerRunRequiresHandler(t *testing.T) {
	var writer bytes.Buffer
	server := NewServer(bytes.NewReader(nil), &writer, nil, nil, testOrigin(), testOrigin())
	if err := server.Run(context.Background()); !errors.Is(err, ErrInvalidHandler) {
		t.Fatalf("Run error = %v, want ErrInvalidHandler", err)
	}
}

func TestServerRunHonorsContextCancellation(t *testing.T) {
	handler := &recordingHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var writer bytes.Buffer
	server := NewServer(bytes.NewReader(nil), &writer, handler, nil, testOrigin(), testOrigin())
	if err := server.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if handler.callCount() != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.callCount())
	}
}

func TestServerRunDisconnectsOnWriteFailure(t *testing.T) {
	payload := []byte(`{"type":"reset","protocolVersion":1,"requestId":"req-1"}`)
	handler := &recordingHandler{}
	lifecycle := &recordingLifecycle{}
	server := NewServer(bytes.NewReader(frame(payload)), errorWriter{}, handler, lifecycle, testOrigin(), testOrigin())
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run must fail when the writer fails")
	}
	if _, disconnects := lifecycle.counts(); disconnects != 1 {
		t.Fatalf("disconnects = %d, want 1", disconnects)
	}
}

func TestServerRunWritesOnlyFramedProtocolData(t *testing.T) {
	payload := []byte(`{"type":"status_request","protocolVersion":1,"requestId":"req-1"}`)
	handler := &recordingHandler{}
	writer, err := runServer(t, bytes.NewReader(frame(payload)), handler, nil)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Run error = %v, want io.EOF", err)
	}
	frames := decodeAllFrames(t, writer.Bytes())
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if _, err := protocol.DecodeResponse(frames[0]); err != nil {
		t.Fatalf("stdout frame is not a valid protocol response: %v", err)
	}
	if bytes.Contains(writer.Bytes(), []byte("log")) {
		t.Fatalf("stdout must contain only framed protocol data: %q", writer.Bytes())
	}
}
