package protocol

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRequestID = "req-1"

func testTranscript() string {
	return "Welcome to the lecture. Today we cover database indexes, " +
		"query planning, transactions, and recovery in detail."
}

func validTestJob() TranscriptJob {
	return TranscriptJob{
		SchemaVersion:         1,
		LectureKey:            "eecs484/2026-fall/001",
		CourseSlug:            "eecs484",
		CourseName:            "EECS 484",
		Term:                  "2026-fall",
		LectureNumber:         1,
		LectureDate:           "2026-09-01",
		SourceURL:             "https://leccap.engin.umich.edu/leccap/player/r/abc123",
		CapturedAt:            "2026-09-20T12:34:56Z",
		Transcript:            testTranscript(),
		TimestampedTranscript: "[00:00] " + testTranscript(),
		ContentHash:           strings.Repeat("a", 64),
	}
}

func int64Pointer(value int64) *int64 { return &value }

func intPointer(value int) *int { return &value }

func stringPointer(value string) *string { return &value }

func assertCategory(t *testing.T, err error, category ErrorCategory) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", category)
	}
	var protocolErr ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("expected ProtocolError %s, got %T: %v", category, err, err)
	}
	if protocolErr.Category != category {
		t.Fatalf("expected category %s, got %s (%v)", category, protocolErr.Category, err)
	}
	if err.Error() != string(category) {
		t.Fatalf("error text %q must be category-only", err.Error())
	}
}

func readProtocolFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "protocol", name))
	if err != nil {
		t.Fatalf("read protocol/%s: %v", name, err)
	}
	return data
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(data)
}

type requestCase struct {
	name    string
	payload string
	want    ErrorCategory
	check   func(t *testing.T, request Request)
}

func runRequestCases(t *testing.T, cases []requestCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := DecodeRequest([]byte(testCase.payload))
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("DecodeRequest(%s) unexpected error: %v", testCase.payload, err)
				}
			} else {
				assertCategory(t, err, testCase.want)
			}
			if testCase.check != nil {
				testCase.check(t, request)
			}
		})
	}
}
