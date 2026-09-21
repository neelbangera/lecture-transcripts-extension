package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	requestIDPattern  = regexp.MustCompile(`^[\x20-\x7e]{1,64}$`)
	versionPattern    = regexp.MustCompile(`^[\x20-\x7e]{1,32}$`)
	hashPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	lectureKeyPattern = regexp.MustCompile(`^[a-z0-9]+/[0-9]{4}-(winter|spring|summer|fall)/[0-9]{3}$`)
	courseSlugPattern = regexp.MustCompile(`^[a-z0-9]+$`)
	termPattern       = regexp.MustCompile(`^[0-9]{4}-(winter|spring|summer|fall)$`)
)

// DecodeRequest is the strict Native Messaging request decoder.  It checks
// the envelope before decoding a TranscriptJob, rejects unknown fields, and
// returns a partially populated Request so a submit rejection can still echo
// a valid requestId and safe identity/hash metadata when available.
func DecodeRequest(data []byte) (Request, error) {
	var request Request
	if len(data) > MaxFrameBytes {
		return request, validationError(ErrorRejectedOversized)
	}

	object, err := strictJSONObject(data)
	if err != nil {
		return request, validationError(ErrorInvalidMessage)
	}

	typeValue, ok := object["type"]
	if !ok {
		return request, validationError(ErrorRejectedInvalidSchema)
	}
	requestType, err := decodeString(typeValue)
	if err != nil {
		return request, validationError(ErrorRejectedInvalidSchema)
	}
	request.Type = RequestType(requestType)

	if value, ok := object["protocolVersion"]; ok {
		request.ProtocolVersion, err = decodeInt(value)
		if err != nil {
			return request, validationError(ErrorRejectedInvalidSchema)
		}
	} else {
		return request, validationError(ErrorRejectedInvalidSchema)
	}

	if value, ok := object["requestId"]; ok {
		request.RequestID, err = decodeString(value)
		if err != nil || !requestIDPattern.MatchString(request.RequestID) {
			return request, validationError(ErrorRejectedInvalidSchema)
		}
	} else {
		return request, validationError(ErrorRejectedInvalidSchema)
	}

	if request.ProtocolVersion != ProtocolVersion {
		return request, validationError(ErrorProtocolMismatch)
	}

	baseKeys := map[string]struct{}{
		"type": {}, "protocolVersion": {}, "requestId": {},
	}

	switch request.Type {
	case RequestConnect:
		if err := requireKeys(object, mergeKeys(baseKeys, "extensionVersion"), "extensionVersion"); err != nil {
			return request, err
		}
		request.ExtensionVersion, err = decodeString(object["extensionVersion"])
		if err != nil || !versionPattern.MatchString(request.ExtensionVersion) {
			return request, validationError(ErrorRejectedInvalidSchema)
		}
	case RequestSubmitJob:
		if err := requireKeys(object, mergeKeys(baseKeys, "job"), "job"); err != nil {
			return request, err
		}
		job, jobErr := decodeJob(object["job"])
		request.Job = &job
		if jobErr != nil {
			return request, jobErr
		}
	case RequestStatus:
		if err := requireKeys(object, mergeKeys(baseKeys, "beforeJobId", "limit")); err != nil {
			return request, err
		}
		if value, ok := object["beforeJobId"]; ok {
			cursor, cursorErr := decodeInt64(value)
			if cursorErr != nil || cursor <= 0 {
				return request, validationError(ErrorRejectedInvalidSchema)
			}
			request.BeforeJobID = &cursor
		}
		if value, ok := object["limit"]; ok {
			limit, limitErr := decodeInt(value)
			if limitErr != nil || limit < 1 || limit > 50 {
				return request, validationError(ErrorRejectedInvalidSchema)
			}
			request.Limit = &limit
		}
	case RequestRetryJob:
		if err := requireKeys(object, mergeKeys(baseKeys, "jobId"), "jobId"); err != nil {
			return request, err
		}
		jobID, jobErr := decodeInt64(object["jobId"])
		if jobErr != nil || jobID <= 0 {
			return request, validationError(ErrorRejectedInvalidSchema)
		}
		request.JobID = &jobID
	case RequestDiscardJob:
		if err := requireKeys(object, mergeKeys(baseKeys, "jobId", "confirmation"), "jobId", "confirmation"); err != nil {
			return request, err
		}
		jobID, jobErr := decodeInt64(object["jobId"])
		if jobErr != nil || jobID <= 0 {
			return request, validationError(ErrorRejectedInvalidSchema)
		}
		request.JobID = &jobID
		request.Confirmation, err = decodeString(object["confirmation"])
		if err != nil || request.Confirmation != "discard" {
			return request, validationError(ErrorRejectedInvalidSchema)
		}
	case RequestReset:
		if err := requireKeys(object, baseKeys); err != nil {
			return request, err
		}
	default:
		return request, validationError(ErrorRejectedInvalidSchema)
	}

	return request, nil
}

// transcriptJobKeys is the schema-required field list for TranscriptJob.  The
// key and the value are both required: a missing key must not silently decode
// to a zero value that happens to be valid (for example an empty
// timestampedTranscript).
var transcriptJobKeys = []string{
	"schemaVersion", "lectureKey", "courseSlug", "courseName", "term",
	"lectureNumber", "lectureDate", "sourceUrl", "capturedAt", "transcript",
	"timestampedTranscript", "contentHash",
}

func decodeJob(data []byte) (TranscriptJob, error) {
	var job TranscriptJob
	object, err := strictJSONObject(data)
	if err != nil {
		return job, validationError(ErrorRejectedInvalidSchema)
	}
	allowed := make(map[string]struct{}, len(transcriptJobKeys))
	for _, key := range transcriptJobKeys {
		allowed[key] = struct{}{}
	}
	if err := requireKeys(object, allowed, transcriptJobKeys...); err != nil {
		return job, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&job); err != nil {
		return job, validationError(ErrorRejectedInvalidSchema)
	}
	if err := requireEOF(decoder); err != nil {
		return job, validationError(ErrorRejectedInvalidSchema)
	}
	if err := ValidateJob(job); err != nil {
		return job, err
	}
	return job, nil
}

// ValidateJob performs the runtime checks that JSON Schema cannot express:
// UTF-8 byte limits, real dates, lecture-key consistency, source URL safety,
// and the minimum useful transcript size.
func ValidateJob(job TranscriptJob) error {
	if job.SchemaVersion != ProtocolVersion {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if !lectureKeyPattern.MatchString(job.LectureKey) || runeLen(job.LectureKey) > 128 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if !courseSlugPattern.MatchString(job.CourseSlug) || runeLen(job.CourseSlug) > 64 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if !nonEmptyBounded(job.CourseName, 256) || strings.ContainsAny(job.CourseName, "\r\n") {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if !termPattern.MatchString(job.Term) || runeLen(job.Term) > 32 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if expectedKey := job.CourseSlug + "/" + job.Term + "/" + zeroPadLecture(job.LectureNumber); expectedKey != job.LectureKey {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if job.LectureNumber < 1 || job.LectureNumber > 999 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if !validCalendarDate(job.LectureDate) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	canonicalSource, sourceErr := CanonicalizeSourceURL(job.SourceURL)
	if sourceErr != nil {
		return validationError(ErrorRejectedUnsafeURL)
	}
	if len(canonicalSource) > MaxSourceURLBytes {
		return validationError(ErrorRejectedUnsafeURL)
	}
	if !validUTCSecond(job.CapturedAt) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if !utf8.ValidString(job.Transcript) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if len([]byte(job.Transcript)) > MaxTranscriptBytes {
		return validationError(ErrorRejectedOversized)
	}
	if nonWhitespaceRuneCount(job.Transcript) < 50 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if !utf8.ValidString(job.TimestampedTranscript) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if len([]byte(job.TimestampedTranscript)) > MaxTranscriptBytes {
		return validationError(ErrorRejectedOversized)
	}
	if !hashPattern.MatchString(job.ContentHash) {
		return validationError(ErrorRejectedInvalidHash)
	}

	serialized, err := job.MarshalCanonical()
	if err != nil {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if len(serialized) > MaxSerializedJobByte {
		return validationError(ErrorRejectedOversized)
	}
	return nil
}

// CanonicalizeJob applies the wire-level source URL canonicalization without
// changing any transcript or identity field.  Queue code should call this
// before persisting/rendering a validated job.
func CanonicalizeJob(job TranscriptJob) (TranscriptJob, error) {
	canonical, err := CanonicalizeSourceURL(job.SourceURL)
	if err != nil {
		return job, validationError(ErrorRejectedUnsafeURL)
	}
	job.SourceURL = canonical
	if err := ValidateJob(job); err != nil {
		return job, err
	}
	return job, nil
}

// CanonicalizeSourceURL strips query and fragment data, normalizes the HTTPS
// host, and accepts only the designated Leccap host.  A path containing a
// sensitive token segment is still a valid source URL; the Markdown renderer
// decides to omit that canonical URL from public metadata.
func CanonicalizeSourceURL(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\r\n") || !utf8.ValidString(raw) {
		return "", errors.New("invalid source url")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return "", errors.New("invalid source url")
	}
	if !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "leccap.engin.umich.edu") {
		return "", errors.New("unsafe source url")
	}
	if port := u.Port(); port != "" && port != "443" {
		return "", errors.New("unsafe source url")
	}
	pathValue := u.EscapedPath()
	if pathValue == "" {
		pathValue = "/"
	}
	if !strings.HasPrefix(pathValue, "/") {
		return "", errors.New("unsafe source url")
	}
	canonical := "https://leccap.engin.umich.edu" + pathValue
	if len([]byte(canonical)) > MaxSourceURLBytes {
		return "", errors.New("source url too long")
	}
	return canonical, nil
}

// DecodeResponse strictly decodes a response emitted by the uploader.  It is
// useful in tests and is also available to a future host integration test to
// ensure status/command shapes stay closed.
func DecodeResponse(data []byte) (any, error) {
	object, err := strictJSONObject(data)
	if err != nil {
		return nil, validationError(ErrorInvalidMessage)
	}
	typeValue, ok := object["type"]
	if !ok {
		return nil, validationError(ErrorRejectedInvalidSchema)
	}
	typeName, err := decodeString(typeValue)
	if err != nil {
		return nil, validationError(ErrorRejectedInvalidSchema)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value any
	switch typeName {
	case "ack":
		if err := requireResponseKeys(object, ackResponseKeys); err != nil {
			return nil, err
		}
		var message Ack
		if err := decoder.Decode(&message); err != nil {
			return nil, validationError(ErrorRejectedInvalidSchema)
		}
		value = message
	case "command_result":
		if err := requireResponseKeys(object, commandResultResponseKeys); err != nil {
			return nil, err
		}
		var message CommandResult
		if err := decoder.Decode(&message); err != nil {
			return nil, validationError(ErrorRejectedInvalidSchema)
		}
		value = message
	case "status":
		if err := requireResponseKeys(object, statusResponseKeys); err != nil {
			return nil, err
		}
		if err := requireNestedKeys(object["authorization"], authorizationKeys); err != nil {
			return nil, err
		}
		if err := requireNestedKeys(object["counts"], countsKeys); err != nil {
			return nil, err
		}
		if err := requireJobSummaryKeys(object["jobs"]); err != nil {
			return nil, err
		}
		var message StatusMessage
		if err := decoder.Decode(&message); err != nil {
			return nil, validationError(ErrorRejectedInvalidSchema)
		}
		value = message
	case "error":
		if err := requireResponseKeys(object, errorResponseKeys); err != nil {
			return nil, err
		}
		var message ErrorMessage
		if err := decoder.Decode(&message); err != nil {
			return nil, validationError(ErrorRejectedInvalidSchema)
		}
		value = message
	default:
		return nil, validationError(ErrorRejectedInvalidSchema)
	}
	if err := requireEOF(decoder); err != nil {
		return nil, validationError(ErrorRejectedInvalidSchema)
	}
	if err := ValidateResponse(value); err != nil {
		return nil, err
	}
	return value, nil
}

func ValidateResponse(message any) error {
	switch value := message.(type) {
	case Ack:
		return validateAck(value)
	case *Ack:
		if value == nil {
			return validationError(ErrorRejectedInvalidSchema)
		}
		return validateAck(*value)
	case CommandResult:
		return validateCommandResult(value)
	case *CommandResult:
		if value == nil {
			return validationError(ErrorRejectedInvalidSchema)
		}
		return validateCommandResult(*value)
	case StatusMessage:
		return validateStatus(value)
	case *StatusMessage:
		if value == nil {
			return validationError(ErrorRejectedInvalidSchema)
		}
		return validateStatus(*value)
	case ErrorMessage:
		return validateErrorMessage(value)
	case *ErrorMessage:
		if value == nil {
			return validationError(ErrorRejectedInvalidSchema)
		}
		return validateErrorMessage(*value)
	default:
		return errUnsupportedResponse
	}
}

func validateAck(message Ack) error {
	if message.Type != "ack" || message.ProtocolVersion != ProtocolVersion || !requestIDPattern.MatchString(message.RequestID) || message.Operation != "submit" {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if !knownAckStatus(message.Status) || (message.JobID != nil && *message.JobID <= 0) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.LectureKey != nil && (!lectureKeyPattern.MatchString(*message.LectureKey) || runeLen(*message.LectureKey) > 128) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.ContentHash != nil && !hashPattern.MatchString(*message.ContentHash) {
		return validationError(ErrorRejectedInvalidHash)
	}
	if message.ExistingStatus != nil && !IsQueueStatus(*message.ExistingStatus) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.Action != nil && *message.Action != ActionRetryExisting && *message.Action != ActionDiscardExistingThenRecapture {
		return validationError(ErrorRejectedInvalidSchema)
	}
	return nil
}

func validateCommandResult(message CommandResult) error {
	if message.Type != "command_result" || message.ProtocolVersion != ProtocolVersion || !requestIDPattern.MatchString(message.RequestID) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.Operation != "retry" && message.Operation != "discard" && message.Operation != "reset" {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.Result != "accepted" && message.Result != "rejected" {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.JobID != nil && *message.JobID <= 0 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.Status != nil && !validCommandStatus(*message.Status) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.ErrorCategory != nil && !IsErrorCategory(*message.ErrorCategory) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	return nil
}

func validateStatus(message StatusMessage) error {
	if message.Type != "status" || message.ProtocolVersion != ProtocolVersion || !versionPattern.MatchString(message.ExtensionVersion) || !versionPattern.MatchString(message.UploaderVersion) || !IsAuthState(message.AuthState) || !IsDrainState(message.DrainState) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.RequestID != nil && !requestIDPattern.MatchString(*message.RequestID) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if err := validateAuthorization(message.Authorization); err != nil {
		return err
	}
	if err := validateCounts(message.Counts); err != nil {
		return err
	}
	if message.Jobs == nil {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if len(message.Jobs) > 50 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	for _, job := range message.Jobs {
		if err := validateJobSummary(job); err != nil {
			return err
		}
	}
	if message.NextBeforeJobID != nil && *message.NextBeforeJobID <= 0 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	return nil
}

func validateAuthorization(auth Authorization) error {
	if auth.UserCode != nil && !versionLikeBounded(*auth.UserCode, 64) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	for _, value := range []*string{auth.VerificationURI, auth.VerificationURIComplete} {
		if value == nil {
			continue
		}
		u, err := url.Parse(*value)
		if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "github.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len([]byte(*value)) > 2048 {
			return validationError(ErrorRejectedInvalidSchema)
		}
	}
	if auth.ExpiresAt != nil && !validUTCSecond(*auth.ExpiresAt) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	return nil
}

func validateCounts(counts QueueCounts) error {
	values := []int{
		counts.Queued, counts.Uploading, counts.Uploaded, counts.Unchanged,
		counts.RetryableError, counts.PermanentConflict,
		counts.RejectedMissingIdentity, counts.RejectedAmbiguousMetadata,
		counts.RejectedOversized, counts.RejectedQueueFull,
		counts.RejectedInvalidHash, counts.RejectedUnsafeURL,
		counts.RejectedUnknownField, counts.RejectedInvalidSchema,
		counts.RejectedPermission,
	}
	for _, value := range values {
		if value < 0 {
			return validationError(ErrorRejectedInvalidSchema)
		}
	}
	return nil
}

func validateJobSummary(job JobSummary) error {
	if job.JobID <= 0 || !lectureKeyPattern.MatchString(job.LectureKey) || runeLen(job.LectureKey) > 128 || !hashPattern.MatchString(job.ContentHash) || !IsQueueStatus(job.Status) || job.AttemptCount < 0 || job.TargetPath == "" || runeLen(job.TargetPath) > 512 {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if err := validateOptionalTimestamp(job.NextAttemptAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp(job.UpdatedAt); err != nil {
		return err
	}
	if job.LastErrorCategory != nil && (runeLen(*job.LastErrorCategory) > 64 || !IsErrorCategory(ErrorCategory(*job.LastErrorCategory))) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if job.LastErrorHTTPStatus != nil && (*job.LastErrorHTTPStatus < 100 || *job.LastErrorHTTPStatus > 599) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if job.RemoteContentHash != nil && !hashPattern.MatchString(*job.RemoteContentHash) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if job.RemoteFileKind != nil && !IsRemoteFileKind(*job.RemoteFileKind) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	return nil
}

func validateOptionalTimestamp(value *string) error {
	if value != nil && !validUTCSecond(*value) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	return nil
}

func validateErrorMessage(message ErrorMessage) error {
	if message.Type != "error" || message.ProtocolVersion != ProtocolVersion || !IsErrorCategory(message.Category) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	if message.RequestID != nil && !requestIDPattern.MatchString(*message.RequestID) {
		return validationError(ErrorRejectedInvalidSchema)
	}
	return nil
}

func IsQueueStatus(status QueueStatus) bool {
	switch status {
	case StatusQueued, StatusUploading, StatusUploaded, StatusUnchanged,
		StatusRetryableError, StatusPermanentConflict, StatusRejectedMissingIdentity,
		StatusRejectedAmbiguousMetadata, StatusRejectedOversized, StatusRejectedQueueFull,
		StatusRejectedInvalidHash, StatusRejectedUnsafeURL, StatusRejectedUnknownField,
		StatusRejectedInvalidSchema, StatusRejectedPermission:
		return true
	default:
		return false
	}
}

func IsErrorCategory(category ErrorCategory) bool {
	switch category {
	case ErrorProtocolMismatch, ErrorInvalidMessage, ErrorHostUnavailable,
		ErrorInvalidState, ErrorNotConnected, ErrorReauthorizationRequired,
		ErrorTargetRepositoryUnavailable, ErrorInternal, ErrorIneligibleCommand,
		ErrorRejectedMissingIdentity, ErrorRejectedAmbiguousMetadata,
		ErrorRejectedOversized, ErrorRejectedQueueFull, ErrorRejectedHandoffFull,
		ErrorRejectedInvalidHash, ErrorRejectedUnsafeURL, ErrorRejectedUnknownField,
		ErrorRejectedInvalidSchema, ErrorRejectedPermission:
		return true
	default:
		return false
	}
}

func IsAuthState(state AuthState) bool {
	switch state {
	case AuthNotConnected, AuthAuthorizing, AuthConnected,
		AuthReauthorizationRequired, AuthTargetRepositoryUnavailable, AuthProtocolMismatch:
		return true
	default:
		return false
	}
}

func IsDrainState(state DrainState) bool {
	switch state {
	case DrainIdle, DrainWorking, DrainWaitingForBackoff, DrainAuthorizing:
		return true
	default:
		return false
	}
}

func IsRemoteFileKind(kind RemoteFileKind) bool {
	switch kind {
	case RemoteFile, RemoteDirectory, RemoteSymlink, RemoteSubmodule, RemoteMalformed, RemoteMissing:
		return true
	default:
		return false
	}
}

func IsValidLectureKey(value string) bool {
	return lectureKeyPattern.MatchString(value) && runeLen(value) <= 128
}

func IsValidContentHash(value string) bool {
	return hashPattern.MatchString(value)
}

func knownAckStatus(status AckStatus) bool {
	switch status {
	case AckQueued, AckAlreadyQueued, AckRejectedInvalidSchema, AckRejectedUnknownField,
		AckRejectedOversized, AckRejectedInvalidHash, AckRejectedUnsafeURL,
		AckRejectedQueueFull, AckRejectedDuplicateTerminal:
		return true
	default:
		return false
	}
}

func validCommandStatus(status string) bool {
	if status == "discarded" || status == "reset" {
		return true
	}
	return IsQueueStatus(QueueStatus(status))
}

func validationError(category ErrorCategory) error {
	return ProtocolError{Category: category}
}

func mergeKeys(base map[string]struct{}, extra ...string) map[string]struct{} {
	merged := make(map[string]struct{}, len(base)+len(extra))
	for key := range base {
		merged[key] = struct{}{}
	}
	for _, key := range extra {
		merged[key] = struct{}{}
	}
	return merged
}

func requireKeys(object map[string]json.RawMessage, allowed map[string]struct{}, required ...string) error {
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return validationError(ErrorRejectedUnknownField)
		}
	}
	for _, key := range required {
		if _, ok := object[key]; !ok {
			return validationError(ErrorRejectedInvalidSchema)
		}
	}
	return nil
}

// Every response property is schema-required, so presence must be checked on
// the raw object before decoding into a struct; a missing key and a null key
// both decode to the same Go zero value.
var (
	ackResponseKeys = []string{
		"type", "protocolVersion", "requestId", "operation", "jobId",
		"lectureKey", "contentHash", "status", "existingStatus", "action",
	}
	commandResultResponseKeys = []string{
		"type", "protocolVersion", "requestId", "operation", "jobId",
		"result", "status", "errorCategory",
	}
	statusResponseKeys = []string{
		"type", "protocolVersion", "requestId", "extensionVersion",
		"uploaderVersion", "authState", "authorization", "drainState",
		"counts", "jobs", "nextBeforeJobId",
	}
	errorResponseKeys = []string{
		"type", "protocolVersion", "requestId", "category", "retryable",
	}
	authorizationKeys = []string{
		"userCode", "verificationUri", "verificationUriComplete", "expiresAt",
	}
	countsKeys = []string{
		"queued", "uploading", "uploaded", "unchanged", "retryable_error",
		"permanent_conflict", "rejected_missing_identity",
		"rejected_ambiguous_metadata", "rejected_oversized",
		"rejected_queue_full", "rejected_invalid_hash", "rejected_unsafe_url",
		"rejected_unknown_field", "rejected_invalid_schema",
		"rejected_permission",
	}
	jobSummaryKeys = []string{
		"jobId", "lectureKey", "contentHash", "status", "attemptCount",
		"nextAttemptAt", "updatedAt", "targetPath", "lastErrorCategory",
		"lastErrorHttpStatus", "remoteContentHash", "remoteFileKind",
	}
)

func requireResponseKeys(object map[string]json.RawMessage, keys []string) error {
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	return requireKeys(object, allowed, keys...)
}

func requireNestedKeys(raw json.RawMessage, keys []string) error {
	object, err := strictJSONObject(raw)
	if err != nil {
		return validationError(ErrorRejectedInvalidSchema)
	}
	return requireResponseKeys(object, keys)
}

func requireJobSummaryKeys(raw json.RawMessage) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return validationError(ErrorRejectedInvalidSchema)
	}
	for _, item := range items {
		if err := requireNestedKeys(item, jobSummaryKeys); err != nil {
			return err
		}
	}
	return nil
}

func strictJSONObject(data []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := rejectDuplicateKeys(decoder); err != nil {
		return nil, err
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, errors.New("expected JSON object")
	}
	if err := requireEOF(decoder); err != nil {
		return nil, err
	}
	return object, nil
}

func rejectDuplicateKeys(decoder *json.Decoder) error {
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return keyErr
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("invalid object key")
				}
				if _, exists := seen[key]; exists {
					return errors.New("duplicate object key")
				}
				seen[key] = struct{}{}
				if err := walkJSONValue(decoder); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walkJSONValue(decoder); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("unexpected JSON delimiter")
		}
	default:
		return nil
	}
}

func decodeString(data []byte) (string, error) {
	var value string
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) || json.Unmarshal(data, &value) != nil {
		return "", errors.New("expected string")
	}
	return value, nil
}

func decodeInt(data []byte) (int, error) {
	value, err := decodeInt64(data)
	if err != nil || int64(int(value)) != value {
		return 0, errors.New("expected integer")
	}
	return int(value), nil
}

func decodeInt64(data []byte) (int64, error) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return 0, errors.New("expected integer")
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return 0, errors.New("expected integer")
	}
	return value, nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func nonEmptyBounded(value string, maxRunes int) bool {
	return value != "" && utf8.ValidString(value) && runeLen(value) <= maxRunes
}

func runeLen(value string) int { return utf8.RuneCountInString(value) }

func nonWhitespaceRuneCount(value string) int {
	count := 0
	for _, r := range value {
		if !unicode.IsSpace(r) {
			count++
		}
	}
	return count
}

func zeroPadLecture(number int) string {
	if number < 0 || number > 999 {
		return ""
	}
	return string([]byte{'0' + byte(number/100), '0' + byte((number/10)%10), '0' + byte(number%10)})
}

func validCalendarDate(value string) bool {
	if !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`).MatchString(value) {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validUTCSecond(value string) bool {
	if !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`).MatchString(value) {
		return false
	}
	parsed, err := time.Parse("2006-01-02T15:04:05Z", value)
	return err == nil && parsed.UTC().Format("2006-01-02T15:04:05Z") == value
}

func versionLikeBounded(value string, maxRunes int) bool {
	return versionPattern.MatchString(value) && runeLen(value) <= maxRunes
}
