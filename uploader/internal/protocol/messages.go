package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

type RequestType string

const (
	RequestConnect       RequestType = "connect"
	RequestSubmitJob     RequestType = "submit_job"
	RequestStatus        RequestType = "status_request"
	RequestRetryJob      RequestType = "retry_job"
	RequestDiscardJob    RequestType = "discard_job"
	RequestReset         RequestType = "reset"
)

func (t RequestType) String() string { return string(t) }

type QueueStatus string

const (
	StatusQueued                    QueueStatus = "queued"
	StatusUploading                 QueueStatus = "uploading"
	StatusUploaded                  QueueStatus = "uploaded"
	StatusUnchanged                 QueueStatus = "unchanged"
	StatusRetryableError            QueueStatus = "retryable_error"
	StatusPermanentConflict         QueueStatus = "permanent_conflict"
	StatusRejectedMissingIdentity   QueueStatus = "rejected_missing_identity"
	StatusRejectedAmbiguousMetadata QueueStatus = "rejected_ambiguous_metadata"
	StatusRejectedOversized         QueueStatus = "rejected_oversized"
	StatusRejectedQueueFull         QueueStatus = "rejected_queue_full"
	StatusRejectedInvalidHash       QueueStatus = "rejected_invalid_hash"
	StatusRejectedUnsafeURL         QueueStatus = "rejected_unsafe_url"
	StatusRejectedUnknownField      QueueStatus = "rejected_unknown_field"
	StatusRejectedInvalidSchema     QueueStatus = "rejected_invalid_schema"
	StatusRejectedPermission        QueueStatus = "rejected_permission"
)

type AckStatus string

const (
	AckQueued                    AckStatus = "queued"
	AckAlreadyQueued             AckStatus = "already_queued"
	AckRejectedInvalidSchema     AckStatus = "rejected_invalid_schema"
	AckRejectedUnknownField      AckStatus = "rejected_unknown_field"
	AckRejectedOversized         AckStatus = "rejected_oversized"
	AckRejectedInvalidHash       AckStatus = "rejected_invalid_hash"
	AckRejectedUnsafeURL         AckStatus = "rejected_unsafe_url"
	AckRejectedQueueFull         AckStatus = "rejected_queue_full"
	AckRejectedDuplicateTerminal AckStatus = "rejected_duplicate_terminal"
)

type DuplicateAction string

const (
	ActionRetryExisting              DuplicateAction = "retry_existing"
	ActionDiscardExistingThenRecapture DuplicateAction = "discard_existing_then_recapture"
)

type AuthState string

const (
	AuthNotConnected             AuthState = "not_connected"
	AuthAuthorizing              AuthState = "authorizing"
	AuthConnected                AuthState = "connected"
	AuthReauthorizationRequired  AuthState = "reauthorization_required"
	AuthTargetRepositoryUnavailable AuthState = "target_repository_unavailable"
	AuthProtocolMismatch         AuthState = "protocol_mismatch"
)

type DrainState string

const (
	DrainIdle             DrainState = "idle"
	DrainWorking          DrainState = "working"
	DrainWaitingForBackoff DrainState = "waiting_for_backoff"
	DrainAuthorizing      DrainState = "authorizing"
)

type RemoteFileKind string

const (
	RemoteFile       RemoteFileKind = "file"
	RemoteDirectory  RemoteFileKind = "directory"
	RemoteSymlink    RemoteFileKind = "symlink"
	RemoteSubmodule  RemoteFileKind = "submodule"
	RemoteMalformed  RemoteFileKind = "malformed"
	RemoteMissing    RemoteFileKind = "missing"
)

// ErrorCategory is the complete wire error vocabulary.  It is intentionally
// closed: raw Go errors and remote response bodies must never cross the wire.
type ErrorCategory string

const (
	ErrorProtocolMismatch             ErrorCategory = "protocol_mismatch"
	ErrorInvalidMessage               ErrorCategory = "invalid_message"
	ErrorHostUnavailable              ErrorCategory = "host_unavailable"
	ErrorInvalidState                 ErrorCategory = "invalid_state"
	ErrorNotConnected                 ErrorCategory = "not_connected"
	ErrorReauthorizationRequired      ErrorCategory = "reauthorization_required"
	ErrorTargetRepositoryUnavailable  ErrorCategory = "target_repository_unavailable"
	ErrorInternal                     ErrorCategory = "internal"
	ErrorIneligibleCommand            ErrorCategory = "ineligible_command"
	ErrorRejectedMissingIdentity      ErrorCategory = "rejected_missing_identity"
	ErrorRejectedAmbiguousMetadata    ErrorCategory = "rejected_ambiguous_metadata"
	ErrorRejectedOversized            ErrorCategory = "rejected_oversized"
	ErrorRejectedQueueFull            ErrorCategory = "rejected_queue_full"
	ErrorRejectedHandoffFull          ErrorCategory = "rejected_handoff_full"
	ErrorRejectedInvalidHash          ErrorCategory = "rejected_invalid_hash"
	ErrorRejectedUnsafeURL            ErrorCategory = "rejected_unsafe_url"
	ErrorRejectedUnknownField         ErrorCategory = "rejected_unknown_field"
	ErrorRejectedInvalidSchema        ErrorCategory = "rejected_invalid_schema"
	ErrorRejectedPermission           ErrorCategory = "rejected_permission"
)

// ProtocolError is a safe, classified error suitable for a response.  Its
// Error method is category-only so accidentally printing it cannot reveal
// transcript text, credentials, URLs, or remote response bodies.
type ProtocolError struct {
	Category ErrorCategory
	Retryable bool
}

func (e ProtocolError) Error() string { return string(e.Category) }

func NewProtocolError(category ErrorCategory, retryable bool) error {
	return ProtocolError{Category: category, Retryable: retryable}
}

// Request is the typed form of an extension-to-uploader message.  DecodeRequest
// is the authoritative parser; callers should not decode Native Messaging
// payloads directly with encoding/json.
type Request struct {
	Type            RequestType
	ProtocolVersion int
	RequestID       string
	ExtensionVersion string
	Job             *TranscriptJob
	BeforeJobID     *int64
	Limit           *int
	JobID           *int64
	Confirmation    string
}

func (r Request) EffectiveLimit() int {
	if r.Limit == nil {
		return 50
	}
	return *r.Limit
}

func (r Request) MarshalJSON() ([]byte, error) {
	switch r.Type {
	case RequestConnect:
		return json.Marshal(struct {
			Type            RequestType `json:"type"`
			ProtocolVersion int         `json:"protocolVersion"`
			RequestID       string      `json:"requestId"`
			ExtensionVersion string     `json:"extensionVersion"`
		}{r.Type, r.ProtocolVersion, r.RequestID, r.ExtensionVersion})
	case RequestSubmitJob:
		return json.Marshal(struct {
			Type            RequestType   `json:"type"`
			ProtocolVersion int           `json:"protocolVersion"`
			RequestID       string        `json:"requestId"`
			Job             *TranscriptJob `json:"job"`
		}{r.Type, r.ProtocolVersion, r.RequestID, r.Job})
	case RequestStatus:
		return json.Marshal(struct {
			Type            RequestType `json:"type"`
			ProtocolVersion int         `json:"protocolVersion"`
			RequestID       string      `json:"requestId"`
			BeforeJobID     *int64      `json:"beforeJobId,omitempty"`
			Limit           *int        `json:"limit,omitempty"`
		}{r.Type, r.ProtocolVersion, r.RequestID, r.BeforeJobID, r.Limit})
	case RequestRetryJob:
		return json.Marshal(struct {
			Type            RequestType `json:"type"`
			ProtocolVersion int         `json:"protocolVersion"`
			RequestID       string      `json:"requestId"`
			JobID           int64       `json:"jobId"`
		}{r.Type, r.ProtocolVersion, r.RequestID, valueOrZero(r.JobID)})
	case RequestDiscardJob:
		return json.Marshal(struct {
			Type            RequestType `json:"type"`
			ProtocolVersion int         `json:"protocolVersion"`
			RequestID       string      `json:"requestId"`
			JobID           int64       `json:"jobId"`
			Confirmation    string      `json:"confirmation"`
		}{r.Type, r.ProtocolVersion, r.RequestID, valueOrZero(r.JobID), r.Confirmation})
	case RequestReset:
		return json.Marshal(struct {
			Type            RequestType `json:"type"`
			ProtocolVersion int         `json:"protocolVersion"`
			RequestID       string      `json:"requestId"`
		}{r.Type, r.ProtocolVersion, r.RequestID})
	default:
		return nil, fmt.Errorf("unsupported request type %q", r.Type)
	}
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

type Ack struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocolVersion"`
	RequestID       string          `json:"requestId"`
	Operation       string          `json:"operation"`
	JobID           *int64          `json:"jobId"`
	LectureKey      *string         `json:"lectureKey"`
	ContentHash     *string         `json:"contentHash"`
	Status          AckStatus       `json:"status"`
	ExistingStatus  *QueueStatus    `json:"existingStatus"`
	Action          *DuplicateAction `json:"action"`
}

type CommandResult struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocolVersion"`
	RequestID       string          `json:"requestId"`
	Operation       string          `json:"operation"`
	JobID           *int64          `json:"jobId"`
	Result          string          `json:"result"`
	Status          *string         `json:"status"`
	ErrorCategory   *ErrorCategory  `json:"errorCategory"`
}

type Authorization struct {
	UserCode               *string `json:"userCode"`
	VerificationURI        *string `json:"verificationUri"`
	VerificationURIComplete *string `json:"verificationUriComplete"`
	ExpiresAt              *string `json:"expiresAt"`
}

type QueueCounts struct {
	Queued                    int `json:"queued"`
	Uploading                 int `json:"uploading"`
	Uploaded                  int `json:"uploaded"`
	Unchanged                 int `json:"unchanged"`
	RetryableError            int `json:"retryable_error"`
	PermanentConflict         int `json:"permanent_conflict"`
	RejectedMissingIdentity   int `json:"rejected_missing_identity"`
	RejectedAmbiguousMetadata int `json:"rejected_ambiguous_metadata"`
	RejectedOversized         int `json:"rejected_oversized"`
	RejectedQueueFull         int `json:"rejected_queue_full"`
	RejectedInvalidHash       int `json:"rejected_invalid_hash"`
	RejectedUnsafeURL         int `json:"rejected_unsafe_url"`
	RejectedUnknownField      int `json:"rejected_unknown_field"`
	RejectedInvalidSchema     int `json:"rejected_invalid_schema"`
	RejectedPermission        int `json:"rejected_permission"`
}

type JobSummary struct {
	JobID              int64          `json:"jobId"`
	LectureKey         string         `json:"lectureKey"`
	ContentHash        string         `json:"contentHash"`
	Status             QueueStatus    `json:"status"`
	AttemptCount       int            `json:"attemptCount"`
	NextAttemptAt      *string        `json:"nextAttemptAt"`
	UpdatedAt          *string        `json:"updatedAt"`
	TargetPath         string         `json:"targetPath"`
	LastErrorCategory  *string        `json:"lastErrorCategory"`
	LastErrorHTTPStatus *int          `json:"lastErrorHttpStatus"`
	RemoteContentHash  *string        `json:"remoteContentHash"`
	RemoteFileKind     *RemoteFileKind `json:"remoteFileKind"`
}

type StatusMessage struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocolVersion"`
	RequestID       *string        `json:"requestId"`
	ExtensionVersion string        `json:"extensionVersion"`
	UploaderVersion  string        `json:"uploaderVersion"`
	AuthState       AuthState      `json:"authState"`
	Authorization  Authorization  `json:"authorization"`
	DrainState      DrainState     `json:"drainState"`
	Counts          QueueCounts    `json:"counts"`
	Jobs            []JobSummary   `json:"jobs"`
	NextBeforeJobID *int64         `json:"nextBeforeJobId"`
}

type ErrorMessage struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocolVersion"`
	RequestID       *string        `json:"requestId"`
	Category        ErrorCategory  `json:"category"`
	Retryable       bool           `json:"retryable"`
}

// EncodeResponse validates the closed response vocabulary before serializing
// it.  This is the only encoder the host should use for application responses.
func EncodeResponse(message any) ([]byte, error) {
	if err := ValidateResponse(message); err != nil {
		return nil, err
	}
	return json.Marshal(message)
}

var errUnsupportedResponse = errors.New("unsupported response type")

