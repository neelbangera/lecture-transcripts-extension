package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func validTestJobSummary() JobSummary {
	updatedAt := "2026-09-20T12:34:56Z"
	httpStatus := 404
	return JobSummary{
		JobID:               7,
		LectureKey:          "eecs484/2026-fall/001",
		ContentHash:         strings.Repeat("a", 64),
		Status:              StatusQueued,
		AttemptCount:        0,
		NextAttemptAt:       nil,
		UpdatedAt:           &updatedAt,
		TargetPath:          "eecs484/lectures/001.md",
		LastErrorCategory:   nil,
		LastErrorHTTPStatus: &httpStatus,
		RemoteContentHash:   nil,
		RemoteFileKind:      nil,
	}
}

func validStatusMessage() StatusMessage {
	return StatusMessage{
		Type:             "status",
		ProtocolVersion:  ProtocolVersion,
		ExtensionVersion: "0.1.0",
		UploaderVersion:  "0.2.0",
		AuthState:        AuthNotConnected,
		Authorization:    Authorization{},
		DrainState:       DrainIdle,
		Counts:           QueueCounts{},
		Jobs:             []JobSummary{},
	}
}

func TestEncodeDecodeResponseRoundTrips(t *testing.T) {
	requestID := testRequestID
	jobID := int64(7)
	lectureKey := "eecs484/2026-fall/001"
	contentHash := strings.Repeat("a", 64)
	queuedStatus := StatusQueued
	errorCategory := ErrorIneligibleCommand
	userCode := "ABCD-1234"
	verificationURI := "https://github.com/login/device"
	expiresAt := "2026-09-20T13:00:00Z"
	retryStatus := "discarded"
	resetStatus := "reset"

	statusWithJob := validStatusMessage()
	statusWithJob.RequestID = &requestID
	statusWithJob.Jobs = []JobSummary{validTestJobSummary()}
	nextCursor := int64(7)
	statusWithJob.NextBeforeJobID = &nextCursor

	statusAuthorizing := validStatusMessage()
	statusAuthorizing.AuthState = AuthAuthorizing
	statusAuthorizing.DrainState = DrainAuthorizing
	statusAuthorizing.Authorization = Authorization{
		UserCode:                &userCode,
		VerificationURI:         &verificationURI,
		VerificationURIComplete: &verificationURI,
		ExpiresAt:               &expiresAt,
	}
	statusAuthorizing.Counts = QueueCounts{Queued: 2, RetryableError: 1}

	cases := []any{
		Ack{
			Type: "ack", ProtocolVersion: ProtocolVersion, RequestID: requestID,
			Operation: "submit", JobID: &jobID, LectureKey: &lectureKey,
			ContentHash: &contentHash, Status: AckQueued,
		},
		Ack{
			Type: "ack", ProtocolVersion: ProtocolVersion, RequestID: requestID,
			Operation: "submit", Status: AckAlreadyQueued, ExistingStatus: &queuedStatus,
		},
		Ack{
			Type: "ack", ProtocolVersion: ProtocolVersion, RequestID: requestID,
			Operation: "submit", Status: AckRejectedInvalidHash,
		},
		Ack{
			Type: "ack", ProtocolVersion: ProtocolVersion, RequestID: requestID,
			Operation: "submit", JobID: &jobID, LectureKey: &lectureKey,
			ContentHash: &contentHash, Status: AckRejectedDuplicateTerminal,
			ExistingStatus: &queuedStatus,
		},
		CommandResult{
			Type: "command_result", ProtocolVersion: ProtocolVersion, RequestID: requestID,
			Operation: "retry", JobID: &jobID, Result: "accepted", Status: stringPointer("queued"),
		},
		CommandResult{
			Type: "command_result", ProtocolVersion: ProtocolVersion, RequestID: requestID,
			Operation: "retry", JobID: &jobID, Result: "rejected",
			Status: stringPointer(string(StatusPermanentConflict)), ErrorCategory: &errorCategory,
		},
		CommandResult{
			Type: "command_result", ProtocolVersion: ProtocolVersion, RequestID: requestID,
			Operation: "discard", JobID: &jobID, Result: "accepted", Status: &retryStatus,
		},
		CommandResult{
			Type: "command_result", ProtocolVersion: ProtocolVersion, RequestID: requestID,
			Operation: "reset", Result: "accepted", Status: &resetStatus,
		},
		validStatusMessage(),
		statusWithJob,
		statusAuthorizing,
		ErrorMessage{
			Type: "error", ProtocolVersion: ProtocolVersion, RequestID: &requestID,
			Category: ErrorNotConnected, Retryable: true,
		},
		ErrorMessage{
			Type: "error", ProtocolVersion: ProtocolVersion,
			Category: ErrorInternal, Retryable: false,
		},
	}
	for _, message := range cases {
		encoded, err := EncodeResponse(message)
		if err != nil {
			t.Fatalf("EncodeResponse(%+v): %v", message, err)
		}
		decoded, err := DecodeResponse(encoded)
		if err != nil {
			t.Fatalf("DecodeResponse(%s): %v", encoded, err)
		}
		if !reflect.DeepEqual(decoded, message) {
			t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", decoded, message)
		}
	}
}

func TestEncodeResponseRejectsInvalidMessages(t *testing.T) {
	requestID := testRequestID
	badStatus := QueueStatus("bogus")
	badAction := DuplicateAction("bogus")
	badCategory := ErrorCategory("bogus")
	badOperationStatus := "bogus"

	messages := []struct {
		name    string
		message any
		want    ErrorCategory
	}{
		{"ack unknown status", Ack{Type: "ack", ProtocolVersion: 1, RequestID: requestID, Operation: "submit", Status: "bogus"}, ErrorRejectedInvalidSchema},
		{"ack wrong version", Ack{Type: "ack", ProtocolVersion: 2, RequestID: requestID, Operation: "submit", Status: AckQueued}, ErrorRejectedInvalidSchema},
		{"ack empty request id", Ack{Type: "ack", ProtocolVersion: 1, Operation: "submit", Status: AckQueued}, ErrorRejectedInvalidSchema},
		{"ack wrong operation", Ack{Type: "ack", ProtocolVersion: 1, RequestID: requestID, Operation: "retry", Status: AckQueued}, ErrorRejectedInvalidSchema},
		{"ack zero job id", Ack{Type: "ack", ProtocolVersion: 1, RequestID: requestID, Operation: "submit", JobID: int64Pointer(0), Status: AckQueued}, ErrorRejectedInvalidSchema},
		{"ack bad lecture key", Ack{Type: "ack", ProtocolVersion: 1, RequestID: requestID, Operation: "submit", LectureKey: stringPointer("bad"), Status: AckQueued}, ErrorRejectedInvalidSchema},
		{"ack bad content hash", Ack{Type: "ack", ProtocolVersion: 1, RequestID: requestID, Operation: "submit", ContentHash: stringPointer(strings.Repeat("A", 64)), Status: AckQueued}, ErrorRejectedInvalidHash},
		{"ack bad existing status", Ack{Type: "ack", ProtocolVersion: 1, RequestID: requestID, Operation: "submit", ExistingStatus: &badStatus, Status: AckQueued}, ErrorRejectedInvalidSchema},
		{"ack bad action", Ack{Type: "ack", ProtocolVersion: 1, RequestID: requestID, Operation: "submit", Action: &badAction, Status: AckQueued}, ErrorRejectedInvalidSchema},
		{"command wrong operation", CommandResult{Type: "command_result", ProtocolVersion: 1, RequestID: requestID, Operation: "submit", Result: "accepted"}, ErrorRejectedInvalidSchema},
		{"command wrong result", CommandResult{Type: "command_result", ProtocolVersion: 1, RequestID: requestID, Operation: "retry", Result: "maybe"}, ErrorRejectedInvalidSchema},
		{"command zero job id", CommandResult{Type: "command_result", ProtocolVersion: 1, RequestID: requestID, Operation: "retry", JobID: int64Pointer(0), Result: "accepted"}, ErrorRejectedInvalidSchema},
		{"command bad status", CommandResult{Type: "command_result", ProtocolVersion: 1, RequestID: requestID, Operation: "retry", Result: "accepted", Status: &badOperationStatus}, ErrorRejectedInvalidSchema},
		{"command bad error category", CommandResult{Type: "command_result", ProtocolVersion: 1, RequestID: requestID, Operation: "retry", Result: "rejected", ErrorCategory: &badCategory}, ErrorRejectedInvalidSchema},
		{"status nil jobs", func() StatusMessage { s := validStatusMessage(); s.Jobs = nil; return s }(), ErrorRejectedInvalidSchema},
		{"status wrong version", func() StatusMessage { s := validStatusMessage(); s.ProtocolVersion = 2; return s }(), ErrorRejectedInvalidSchema},
		{"status empty extension version", func() StatusMessage { s := validStatusMessage(); s.ExtensionVersion = ""; return s }(), ErrorRejectedInvalidSchema},
		{"status oversized uploader version", func() StatusMessage { s := validStatusMessage(); s.UploaderVersion = strings.Repeat("v", 33); return s }(), ErrorRejectedInvalidSchema},
		{"status bad request id", func() StatusMessage { s := validStatusMessage(); s.RequestID = stringPointer("bad\nid"); return s }(), ErrorRejectedInvalidSchema},
		{"status bad auth state", func() StatusMessage { s := validStatusMessage(); s.AuthState = "bogus"; return s }(), ErrorRejectedInvalidSchema},
		{"status bad drain state", func() StatusMessage { s := validStatusMessage(); s.DrainState = "bogus"; return s }(), ErrorRejectedInvalidSchema},
		{"status negative count", func() StatusMessage { s := validStatusMessage(); s.Counts.Queued = -1; return s }(), ErrorRejectedInvalidSchema},
		{"status too many jobs", func() StatusMessage {
			s := validStatusMessage()
			s.Jobs = make([]JobSummary, 51)
			for index := range s.Jobs {
				s.Jobs[index] = validTestJobSummary()
			}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"status zero next cursor", func() StatusMessage { s := validStatusMessage(); s.NextBeforeJobID = int64Pointer(0); return s }(), ErrorRejectedInvalidSchema},
		{"status http verification uri", func() StatusMessage {
			s := validStatusMessage()
			s.Authorization.VerificationURI = stringPointer("http://github.com/login/device")
			return s
		}(), ErrorRejectedInvalidSchema},
		{"status foreign verification uri", func() StatusMessage {
			s := validStatusMessage()
			s.Authorization.VerificationURI = stringPointer("https://example.com/login")
			return s
		}(), ErrorRejectedInvalidSchema},
		{"status verification uri with userinfo", func() StatusMessage {
			s := validStatusMessage()
			s.Authorization.VerificationURI = stringPointer("https://user@github.com/login")
			return s
		}(), ErrorRejectedInvalidSchema},
		{"status oversized user code", func() StatusMessage {
			s := validStatusMessage()
			s.Authorization.UserCode = stringPointer(strings.Repeat("c", 65))
			return s
		}(), ErrorRejectedInvalidSchema},
		{"status bad expires at", func() StatusMessage {
			s := validStatusMessage()
			s.Authorization.ExpiresAt = stringPointer("2026-09-20T13:00:00.000Z")
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary zero job id", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.JobID = 0
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary bad lecture key", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.LectureKey = "bad"
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary bad hash", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.ContentHash = strings.Repeat("A", 64)
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary bad status", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.Status = "bogus"
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary negative attempts", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.AttemptCount = -1
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary empty target path", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.TargetPath = ""
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary oversized target path", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.TargetPath = strings.Repeat("p", 513)
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary bad timestamp", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.UpdatedAt = stringPointer("2026-09-20 12:34:56")
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary bad error category", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.LastErrorCategory = stringPointer("bogus")
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary bad http status", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.LastErrorHTTPStatus = intPointer(99)
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary bad remote hash", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			summary.RemoteContentHash = stringPointer("bad")
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"summary bad remote file kind", func() StatusMessage {
			s := validStatusMessage()
			summary := validTestJobSummary()
			kind := RemoteFileKind("bogus")
			summary.RemoteFileKind = &kind
			s.Jobs = []JobSummary{summary}
			return s
		}(), ErrorRejectedInvalidSchema},
		{"error bad category", ErrorMessage{Type: "error", ProtocolVersion: 1, Category: "bogus", Retryable: false}, ErrorRejectedInvalidSchema},
		{"error duplicate terminal category", ErrorMessage{Type: "error", ProtocolVersion: 1, Category: "rejected_duplicate_terminal", Retryable: false}, ErrorRejectedInvalidSchema},
		{"error bad request id", ErrorMessage{Type: "error", ProtocolVersion: 1, RequestID: stringPointer("bad\nid"), Category: ErrorInternal, Retryable: false}, ErrorRejectedInvalidSchema},
		{"error wrong version", ErrorMessage{Type: "error", ProtocolVersion: 2, Category: ErrorInternal, Retryable: false}, ErrorRejectedInvalidSchema},
	}
	for _, testCase := range messages {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := EncodeResponse(testCase.message)
			assertCategory(t, err, testCase.want)
		})
	}

	if _, err := EncodeResponse(struct{}{}); err != errUnsupportedResponse {
		t.Fatalf("unsupported response error = %v, want errUnsupportedResponse", err)
	}
	if _, err := EncodeResponse(nil); err != errUnsupportedResponse {
		t.Fatalf("nil response error = %v, want errUnsupportedResponse", err)
	}
	var nilAck *Ack
	if _, err := EncodeResponse(nilAck); err == nil {
		t.Fatal("typed nil response must be rejected")
	}
}

func statusPayloadWithoutKey(t *testing.T, key string) string {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mustJSON(t, validStatusMessage())), &object); err != nil {
		t.Fatalf("json.Unmarshal status: %v", err)
	}
	delete(object, key)
	mutated, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("json.Marshal status: %v", err)
	}
	return string(mutated)
}

func statusPayloadWithoutNestedKey(t *testing.T, parent, key string) string {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mustJSON(t, validStatusMessage())), &object); err != nil {
		t.Fatalf("json.Unmarshal status: %v", err)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(object[parent], &nested); err != nil {
		t.Fatalf("json.Unmarshal status.%s: %v", parent, err)
	}
	delete(nested, key)
	nestedRaw, err := json.Marshal(nested)
	if err != nil {
		t.Fatalf("json.Marshal status.%s: %v", parent, err)
	}
	object[parent] = nestedRaw
	mutated, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("json.Marshal status: %v", err)
	}
	return string(mutated)
}

func TestDecodeResponseRejectsInvalidJSON(t *testing.T) {
	validAck := mustJSON(t, Ack{
		Type: "ack", ProtocolVersion: 1, RequestID: testRequestID, Operation: "submit", Status: AckQueued,
	})
	validStatus := mustJSON(t, validStatusMessage())

	cases := []struct {
		name    string
		payload string
		want    ErrorCategory
	}{
		{name: "empty", payload: "", want: ErrorInvalidMessage},
		{name: "array", payload: "[]", want: ErrorInvalidMessage},
		{name: "invalid utf8", payload: "{\"type\":\"error\",\"protocolVersion\":1,\"requestId\":\"\xff\",\"category\":\"internal\",\"retryable\":false}", want: ErrorInvalidMessage},
		{name: "trailing json", payload: validAck + "{}", want: ErrorInvalidMessage},
		{name: "duplicate keys", payload: strings.Replace(validAck, `"operation":"submit"`, `"operation":"submit","operation":"submit"`, 1), want: ErrorInvalidMessage},
		{name: "missing type", payload: `{"protocolVersion":1}`, want: ErrorRejectedInvalidSchema},
		{name: "unknown type", payload: `{"type":"ping","protocolVersion":1}`, want: ErrorRejectedInvalidSchema},
		{name: "ack unknown field", payload: strings.Replace(validAck, `"status":"queued"`, `"status":"queued","extra":1`, 1), want: ErrorRejectedUnknownField},
		{name: "ack missing action", payload: strings.Replace(validAck, `,"action":null`, "", 1), want: ErrorRejectedInvalidSchema},
		{name: "ack wrong job id type", payload: strings.Replace(validAck, `"jobId":null`, `"jobId":"7"`, 1), want: ErrorRejectedInvalidSchema},
		{name: "status missing counts", payload: statusPayloadWithoutKey(t, "counts"), want: ErrorRejectedInvalidSchema},
		{name: "status missing authorization", payload: statusPayloadWithoutKey(t, "authorization"), want: ErrorRejectedInvalidSchema},
		{name: "status missing counts key", payload: statusPayloadWithoutNestedKey(t, "counts", "queued"), want: ErrorRejectedInvalidSchema},
		{name: "status missing authorization key", payload: statusPayloadWithoutNestedKey(t, "authorization", "userCode"), want: ErrorRejectedInvalidSchema},
		{name: "status null jobs", payload: strings.Replace(validStatus, `"jobs":[]`, `"jobs":null`, 1), want: ErrorRejectedInvalidSchema},
		{name: "status object jobs", payload: strings.Replace(validStatus, `"jobs":[]`, `"jobs":{}`, 1), want: ErrorRejectedInvalidSchema},
		{name: "error missing retryable", payload: `{"type":"error","protocolVersion":1,"requestId":null,"category":"internal"}`, want: ErrorRejectedInvalidSchema},
		{name: "error retryable string", payload: `{"type":"error","protocolVersion":1,"requestId":null,"category":"internal","retryable":"false"}`, want: ErrorRejectedInvalidSchema},
		{name: "error null request id is valid but unknown field is not", payload: `{"type":"error","protocolVersion":1,"requestId":null,"category":"internal","retryable":false,"detail":"raw text"}`, want: ErrorRejectedUnknownField},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DecodeResponse([]byte(testCase.payload))
			assertCategory(t, err, testCase.want)
		})
	}
}

func TestDecodeResponseJobSummaryRequiredKeys(t *testing.T) {
	base := validTestJobSummary()
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("json.Marshal summary: %v", err)
	}
	for _, key := range jobSummaryKeys {
		t.Run("missing "+key, func(t *testing.T) {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatalf("json.Unmarshal summary: %v", err)
			}
			delete(object, key)
			mutated, err := json.Marshal(object)
			if err != nil {
				t.Fatalf("json.Marshal mutated summary: %v", err)
			}
			payload := validStatusWithJobs(t, string(mutated))
			_, err = DecodeResponse([]byte(payload))
			assertCategory(t, err, ErrorRejectedInvalidSchema)
		})
	}
}

func validStatusWithJobs(t *testing.T, summaries ...string) string {
	t.Helper()
	status := validStatusMessage()
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("json.Marshal status: %v", err)
	}
	payload := strings.Replace(string(encoded), `"jobs":[]`, `"jobs":[`+strings.Join(summaries, ",")+`]`, 1)
	if payload == string(encoded) {
		t.Fatalf("status payload did not contain an empty jobs array: %s", encoded)
	}
	return payload
}

func TestDecodeResponseAcceptsNullableFields(t *testing.T) {
	ackPayload := `{"type":"ack","protocolVersion":1,"requestId":"req-1","operation":"submit","jobId":null,"lectureKey":null,"contentHash":null,"status":"already_queued","existingStatus":null,"action":null}`
	decoded, err := DecodeResponse([]byte(ackPayload))
	if err != nil {
		t.Fatalf("DecodeResponse(ack): %v", err)
	}
	ack, ok := decoded.(Ack)
	if !ok || ack.JobID != nil || ack.LectureKey != nil || ack.ContentHash != nil || ack.ExistingStatus != nil || ack.Action != nil {
		t.Fatalf("unexpected ack: %+v", decoded)
	}

	statusPayload := `{"type":"status","protocolVersion":1,"requestId":null,"extensionVersion":"0.1.0","uploaderVersion":"0.2.0","authState":"not_connected","authorization":{"userCode":null,"verificationUri":null,"verificationUriComplete":null,"expiresAt":null},"drainState":"idle","counts":{"queued":0,"uploading":0,"uploaded":0,"unchanged":0,"retryable_error":0,"permanent_conflict":0,"rejected_missing_identity":0,"rejected_ambiguous_metadata":0,"rejected_oversized":0,"rejected_queue_full":0,"rejected_invalid_hash":0,"rejected_unsafe_url":0,"rejected_unknown_field":0,"rejected_invalid_schema":0,"rejected_permission":0},"jobs":[],"nextBeforeJobId":null}`
	decoded, err = DecodeResponse([]byte(statusPayload))
	if err != nil {
		t.Fatalf("DecodeResponse(status): %v", err)
	}
	status, ok := decoded.(StatusMessage)
	if !ok || status.RequestID != nil || status.NextBeforeJobID != nil || status.Jobs == nil || len(status.Jobs) != 0 {
		t.Fatalf("unexpected status: %+v", decoded)
	}

	errorPayload := `{"type":"error","protocolVersion":1,"requestId":null,"category":"internal","retryable":false}`
	if _, err := DecodeResponse([]byte(errorPayload)); err != nil {
		t.Fatalf("DecodeResponse(error): %v", err)
	}
}

func TestResponseErrorTextIsCategoryOnly(t *testing.T) {
	payload := strings.Replace(validStatusWithJobs(t, mustJSON(t, validTestJobSummary())),
		`"status":"queued"`, `"status":"bogus-SECRET-STATUS"`, 1)
	_, err := DecodeResponse([]byte(payload))
	assertCategory(t, err, ErrorRejectedInvalidSchema)
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error %q leaked raw payload text", err.Error())
	}
}

func TestAuthorizationVerificationURIsAllowDeviceFlowQuery(t *testing.T) {
	userCode := "ABCD-1234"
	verificationURI := "https://github.com/login/device"
	verificationURIComplete := "https://github.com/login/device?user_code=ABCD-1234"
	expiresAt := "2026-09-20T12:34:56Z"

	auth := Authorization{
		UserCode:                &userCode,
		VerificationURI:         &verificationURI,
		VerificationURIComplete: &verificationURIComplete,
		ExpiresAt:               &expiresAt,
	}
	if err := validateAuthorization(auth); err != nil {
		t.Fatalf("validateAuthorization: %v", err)
	}

	status := validStatusMessage()
	status.AuthState = AuthAuthorizing
	status.Authorization = auth
	status.DrainState = DrainAuthorizing
	encoded, err := EncodeResponse(status)
	if err != nil {
		t.Fatalf("EncodeResponse(authorizing): %v", err)
	}
	decoded, err := DecodeResponse(encoded)
	if err != nil {
		t.Fatalf("DecodeResponse(authorizing): %v", err)
	}
	roundTripped, ok := decoded.(StatusMessage)
	if !ok || roundTripped.Authorization.VerificationURIComplete == nil || *roundTripped.Authorization.VerificationURIComplete != verificationURIComplete {
		t.Fatalf("unexpected round trip: %+v", decoded)
	}
}
