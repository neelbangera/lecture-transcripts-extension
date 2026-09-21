package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func submitPayload(t *testing.T, job TranscriptJob) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"type":            "submit_job",
		"protocolVersion": 1,
		"requestId":       testRequestID,
		"job":             job,
	})
}

func submitPayloadWithJob(t *testing.T, mutate func(*TranscriptJob)) string {
	t.Helper()
	job := validTestJob()
	mutate(&job)
	return submitPayload(t, job)
}

func TestDecodeRequestConnect(t *testing.T) {
	validPayload := `{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":"0.1.0"}`
	runRequestCases(t, []requestCase{
		{
			name:    "valid",
			payload: validPayload,
			check: func(t *testing.T, request Request) {
				if request.Type != RequestConnect || request.ProtocolVersion != ProtocolVersion ||
					request.RequestID != testRequestID || request.ExtensionVersion != "0.1.0" {
					t.Fatalf("unexpected request: %+v", request)
				}
			},
		},
		{
			name:    "request id at maximum length",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"` + strings.Repeat("x", 64) + `","extensionVersion":"0.1.0"}`,
		},
		{
			name:    "request id with printable space",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"req 1","extensionVersion":"0.1.0"}`,
		},
		{
			name:    "missing extension version",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"req-1"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "empty extension version",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":""}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "extension version too long",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":"` + strings.Repeat("v", 33) + `"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "extension version with newline",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":"0.1.0\n"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "unknown envelope field",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"req-1","extensionVersion":"0.1.0","extra":true}`,
			want:    ErrorRejectedUnknownField,
		},
		{
			name:    "missing protocol version",
			payload: `{"type":"connect","requestId":"req-1","extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "protocol version mismatch",
			payload: `{"type":"connect","protocolVersion":2,"requestId":"req-1","extensionVersion":"0.1.0"}`,
			want:    ErrorProtocolMismatch,
		},
		{
			name:    "protocol version as string",
			payload: `{"type":"connect","protocolVersion":"1","requestId":"req-1","extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "protocol version fractional",
			payload: `{"type":"connect","protocolVersion":1.0,"requestId":"req-1","extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "protocol version null",
			payload: `{"type":"connect","protocolVersion":null,"requestId":"req-1","extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "missing request id",
			payload: `{"type":"connect","protocolVersion":1,"extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "empty request id",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"","extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "request id too long",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"` + strings.Repeat("x", 65) + `","extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "request id with tab",
			payload: `{"type":"connect","protocolVersion":1,"requestId":"req\t1","extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "request id null",
			payload: `{"type":"connect","protocolVersion":1,"requestId":null,"extensionVersion":"0.1.0"}`,
			want:    ErrorRejectedInvalidSchema,
		},
	})
}

func TestDecodeRequestSubmitJob(t *testing.T) {
	runRequestCases(t, []requestCase{
		{
			name:    "valid",
			payload: submitPayload(t, validTestJob()),
			check: func(t *testing.T, request Request) {
				if request.Type != RequestSubmitJob || request.Job == nil {
					t.Fatalf("unexpected request: %+v", request)
				}
				if !reflect.DeepEqual(*request.Job, validTestJob()) {
					t.Fatalf("job round trip mismatch: %+v", *request.Job)
				}
			},
		},
		{
			name:    "missing job",
			payload: `{"type":"submit_job","protocolVersion":1,"requestId":"req-1"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "null job",
			payload: `{"type":"submit_job","protocolVersion":1,"requestId":"req-1","job":null}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "array job",
			payload: `{"type":"submit_job","protocolVersion":1,"requestId":"req-1","job":[]}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "unknown envelope field",
			payload: `{"type":"submit_job","protocolVersion":1,"requestId":"req-1","job":` + mustJSON(t, validTestJob()) + `,"extra":1}`,
			want:    ErrorRejectedUnknownField,
		},
		{
			name: "unknown job field",
			payload: mustJSON(t, map[string]any{
				"type": "submit_job", "protocolVersion": 1, "requestId": testRequestID,
				"job": map[string]any{
					"schemaVersion": 1, "lectureKey": "eecs484/2026-fall/001", "courseSlug": "eecs484",
					"courseName": "EECS 484", "term": "2026-fall", "lectureNumber": 1,
					"lectureDate": "2026-09-01", "sourceUrl": "https://leccap.engin.umich.edu/a",
					"capturedAt": "2026-09-20T12:34:56Z", "transcript": testTranscript(),
					"timestampedTranscript": "", "contentHash": strings.Repeat("a", 64), "extra": true,
				},
			}),
			want: ErrorRejectedUnknownField,
		},
		{
			name: "duplicate envelope key",
			payload: `{"type":"submit_job","type":"submit_job","protocolVersion":1,"requestId":"req-1","job":` +
				mustJSON(t, validTestJob()) + `}`,
			want: ErrorInvalidMessage,
		},
		{
			name: "duplicate job key",
			payload: `{"type":"submit_job","protocolVersion":1,"requestId":"req-1","job":` +
				strings.Replace(mustJSON(t, validTestJob()), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1) + `}`,
			want: ErrorInvalidMessage,
		},
		{
			name:    "trailing json",
			payload: submitPayload(t, validTestJob()) + `{"type":"reset"}`,
			want:    ErrorInvalidMessage,
		},
		{
			name:    "invalid utf8 transcript",
			payload: strings.Replace(submitPayload(t, validTestJob()), "Welcome", "Welc\xffome", 1),
			want:    ErrorInvalidMessage,
		},
		{
			name:    "uppercase content hash",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.ContentHash = strings.Repeat("A", 64) }),
			want:    ErrorRejectedInvalidHash,
		},
		{
			name:    "short content hash",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.ContentHash = strings.Repeat("a", 63) }),
			want:    ErrorRejectedInvalidHash,
		},
		{
			name:    "non hex content hash",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.ContentHash = strings.Repeat("z", 64) }),
			want:    ErrorRejectedInvalidHash,
		},
		{
			name:    "insecure source url",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.SourceURL = "http://leccap.engin.umich.edu/lecture/123" }),
			want:    ErrorRejectedUnsafeURL,
		},
		{
			name:    "foreign source url host",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.SourceURL = "https://example.com/lecture/123" }),
			want:    ErrorRejectedUnsafeURL,
		},
		{
			name:    "lecture key inconsistency",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.LectureKey = "eecs484/2026-fall/002" }),
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "term mismatch",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.Term = "2026-spring" }),
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "lecture number out of bounds",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.LectureNumber = 1000 }),
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "lecture number fractional",
			payload: strings.Replace(submitPayload(t, validTestJob()), `"lectureNumber":1`, `"lectureNumber":1.5`, 1),
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "short transcript",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.Transcript = "too short" }),
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "empty transcript",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.Transcript = "" }),
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "oversized transcript",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.Transcript = strings.Repeat("a", MaxTranscriptBytes+1) }),
			want:    ErrorRejectedOversized,
		},
		{
			name:    "wrong schema version",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.SchemaVersion = 2 }),
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "invalid calendar date",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.LectureDate = "2026-02-30" }),
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "fractional captured at",
			payload: submitPayloadWithJob(t, func(job *TranscriptJob) { job.CapturedAt = "2026-09-20T12:34:56.123Z" }),
			want:    ErrorRejectedInvalidSchema,
		},
	})
}

func TestDecodeRequestSubmitRejectionKeepsCorrelation(t *testing.T) {
	payload := submitPayloadWithJob(t, func(job *TranscriptJob) {
		job.ContentHash = strings.Repeat("A", 64)
	})
	request, err := DecodeRequest([]byte(payload))
	assertCategory(t, err, ErrorRejectedInvalidHash)
	if request.Type != RequestSubmitJob || request.RequestID != testRequestID || request.Job == nil {
		t.Fatalf("rejected submit must keep type, requestId, and job for ack correlation: %+v", request)
	}
	if request.Job.LectureKey != validTestJob().LectureKey {
		t.Fatalf("rejected submit must keep identity metadata: %+v", request.Job)
	}
}

func TestDecodeRequestMissingJobField(t *testing.T) {
	payload := mustJSON(t, map[string]any{
		"type": "submit_job", "protocolVersion": 1, "requestId": testRequestID,
		"job": map[string]any{
			"schemaVersion": 1, "lectureKey": "eecs484/2026-fall/001", "courseSlug": "eecs484",
			"courseName": "EECS 484", "term": "2026-fall", "lectureNumber": 1,
			"lectureDate": "2026-09-01", "sourceUrl": "https://leccap.engin.umich.edu/a",
			"capturedAt": "2026-09-20T12:34:56Z", "transcript": testTranscript(),
			"contentHash": strings.Repeat("a", 64),
		},
	})
	_, err := DecodeRequest([]byte(payload))
	assertCategory(t, err, ErrorRejectedInvalidSchema)
}

func TestDecodeRequestStatus(t *testing.T) {
	runRequestCases(t, []requestCase{
		{
			name:    "valid without cursor",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1"}`,
			check: func(t *testing.T, request Request) {
				if request.Type != RequestStatus || request.BeforeJobID != nil || request.Limit != nil {
					t.Fatalf("unexpected request: %+v", request)
				}
				if request.EffectiveLimit() != 50 {
					t.Fatalf("default limit = %d, want 50", request.EffectiveLimit())
				}
			},
		},
		{
			name:    "valid with cursor and limit",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","beforeJobId":10,"limit":25}`,
			check: func(t *testing.T, request Request) {
				if request.BeforeJobID == nil || *request.BeforeJobID != 10 || request.Limit == nil || *request.Limit != 25 {
					t.Fatalf("unexpected request: %+v", request)
				}
			},
		},
		{
			name:    "minimum limit",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","limit":1}`,
		},
		{
			name:    "maximum limit",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","limit":50}`,
		},
		{
			name:    "zero limit",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","limit":0}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "limit above maximum",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","limit":51}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "fractional limit",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","limit":1.5}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "string limit",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","limit":"5"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "null limit",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","limit":null}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "zero cursor",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","beforeJobId":0}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "negative cursor",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","beforeJobId":-1}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "fractional cursor",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","beforeJobId":1.5}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "string cursor",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","beforeJobId":"10"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "unknown field",
			payload: `{"type":"status_request","protocolVersion":1,"requestId":"req-1","offset":3}`,
			want:    ErrorRejectedUnknownField,
		},
	})
}

func TestDecodeRequestRetryJob(t *testing.T) {
	runRequestCases(t, []requestCase{
		{
			name:    "valid",
			payload: `{"type":"retry_job","protocolVersion":1,"requestId":"req-1","jobId":7}`,
			check: func(t *testing.T, request Request) {
				if request.Type != RequestRetryJob || request.JobID == nil || *request.JobID != 7 {
					t.Fatalf("unexpected request: %+v", request)
				}
			},
		},
		{
			name:    "missing job id",
			payload: `{"type":"retry_job","protocolVersion":1,"requestId":"req-1"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "zero job id",
			payload: `{"type":"retry_job","protocolVersion":1,"requestId":"req-1","jobId":0}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "negative job id",
			payload: `{"type":"retry_job","protocolVersion":1,"requestId":"req-1","jobId":-5}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "fractional job id",
			payload: `{"type":"retry_job","protocolVersion":1,"requestId":"req-1","jobId":1.5}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "string job id",
			payload: `{"type":"retry_job","protocolVersion":1,"requestId":"req-1","jobId":"7"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "unknown field",
			payload: `{"type":"retry_job","protocolVersion":1,"requestId":"req-1","jobId":7,"force":true}`,
			want:    ErrorRejectedUnknownField,
		},
	})
}

func TestDecodeRequestDiscardJob(t *testing.T) {
	runRequestCases(t, []requestCase{
		{
			name:    "valid",
			payload: `{"type":"discard_job","protocolVersion":1,"requestId":"req-1","jobId":7,"confirmation":"discard"}`,
			check: func(t *testing.T, request Request) {
				if request.Type != RequestDiscardJob || request.JobID == nil || *request.JobID != 7 || request.Confirmation != "discard" {
					t.Fatalf("unexpected request: %+v", request)
				}
			},
		},
		{
			name:    "missing confirmation",
			payload: `{"type":"discard_job","protocolVersion":1,"requestId":"req-1","jobId":7}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "wrong confirmation case",
			payload: `{"type":"discard_job","protocolVersion":1,"requestId":"req-1","jobId":7,"confirmation":"Discard"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "wrong confirmation value",
			payload: `{"type":"discard_job","protocolVersion":1,"requestId":"req-1","jobId":7,"confirmation":"yes"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "missing job id",
			payload: `{"type":"discard_job","protocolVersion":1,"requestId":"req-1","confirmation":"discard"}`,
			want:    ErrorRejectedInvalidSchema,
		},
		{
			name:    "zero job id",
			payload: `{"type":"discard_job","protocolVersion":1,"requestId":"req-1","jobId":0,"confirmation":"discard"}`,
			want:    ErrorRejectedInvalidSchema,
		},
	})
}

func TestDecodeRequestReset(t *testing.T) {
	runRequestCases(t, []requestCase{
		{
			name:    "valid",
			payload: `{"type":"reset","protocolVersion":1,"requestId":"req-1"}`,
			check: func(t *testing.T, request Request) {
				if request.Type != RequestReset || request.RequestID != testRequestID {
					t.Fatalf("unexpected request: %+v", request)
				}
			},
		},
		{
			name:    "unknown field",
			payload: `{"type":"reset","protocolVersion":1,"requestId":"req-1","jobId":1}`,
			want:    ErrorRejectedUnknownField,
		},
	})
}

func TestDecodeRequestMalformedEnvelopes(t *testing.T) {
	runRequestCases(t, []requestCase{
		{name: "empty payload", payload: ``, want: ErrorInvalidMessage},
		{name: "whitespace payload", payload: `   `, want: ErrorInvalidMessage},
		{name: "array payload", payload: `[]`, want: ErrorInvalidMessage},
		{name: "string payload", payload: `"connect"`, want: ErrorInvalidMessage},
		{name: "null payload", payload: `null`, want: ErrorInvalidMessage},
		{name: "truncated object", payload: `{"type":"connect"`, want: ErrorInvalidMessage},
		{name: "duplicate type key", payload: `{"type":"connect","type":"reset","protocolVersion":1,"requestId":"req-1","extensionVersion":"0.1.0"}`, want: ErrorInvalidMessage},
		{name: "trailing json", payload: `{"type":"reset","protocolVersion":1,"requestId":"req-1"} {}`, want: ErrorInvalidMessage},
		{name: "invalid utf8", payload: "{\"type\":\"reset\",\"protocolVersion\":1,\"requestId\":\"req-\xff\"}", want: ErrorInvalidMessage},
		{name: "unknown request type", payload: `{"type":"ping","protocolVersion":1,"requestId":"req-1"}`, want: ErrorRejectedInvalidSchema},
		{name: "missing type", payload: `{"protocolVersion":1,"requestId":"req-1"}`, want: ErrorRejectedInvalidSchema},
		{name: "numeric type", payload: `{"type":7,"protocolVersion":1,"requestId":"req-1"}`, want: ErrorRejectedInvalidSchema},
		{
			name:    "payload above frame cap",
			payload: strings.Repeat("a", MaxFrameBytes+1),
			want:    ErrorRejectedOversized,
		},
	})
}

func TestRequestMarshalJSONRoundTrip(t *testing.T) {
	job := validTestJob()
	cases := []Request{
		{Type: RequestConnect, ProtocolVersion: ProtocolVersion, RequestID: testRequestID, ExtensionVersion: "0.1.0"},
		{Type: RequestSubmitJob, ProtocolVersion: ProtocolVersion, RequestID: testRequestID, Job: &job},
		{Type: RequestStatus, ProtocolVersion: ProtocolVersion, RequestID: testRequestID, BeforeJobID: int64Pointer(9), Limit: intPointer(10)},
		{Type: RequestStatus, ProtocolVersion: ProtocolVersion, RequestID: testRequestID},
		{Type: RequestRetryJob, ProtocolVersion: ProtocolVersion, RequestID: testRequestID, JobID: int64Pointer(4)},
		{Type: RequestDiscardJob, ProtocolVersion: ProtocolVersion, RequestID: testRequestID, JobID: int64Pointer(4), Confirmation: "discard"},
		{Type: RequestReset, ProtocolVersion: ProtocolVersion, RequestID: testRequestID},
	}
	for _, original := range cases {
		t.Run(original.Type.String(), func(t *testing.T) {
			encoded, err := original.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if !json.Valid(encoded) {
				t.Fatalf("MarshalJSON produced invalid JSON: %s", encoded)
			}
			decoded, err := DecodeRequest(encoded)
			if err != nil {
				t.Fatalf("DecodeRequest(%s): %v", encoded, err)
			}
			if !reflect.DeepEqual(decoded, original) {
				t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", decoded, original)
			}
		})
	}

	if _, err := (Request{Type: "unsupported"}).MarshalJSON(); err == nil {
		t.Fatal("unsupported request type must fail to marshal")
	}
}

func TestEffectiveLimitDefaultsToFifty(t *testing.T) {
	if got := (Request{}).EffectiveLimit(); got != 50 {
		t.Fatalf("EffectiveLimit() = %d, want 50", got)
	}
	if got := (Request{Limit: intPointer(7)}).EffectiveLimit(); got != 7 {
		t.Fatalf("EffectiveLimit() = %d, want 7", got)
	}
}
