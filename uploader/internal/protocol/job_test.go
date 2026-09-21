package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type jobCase struct {
	name   string
	mutate func(*TranscriptJob)
	want   ErrorCategory
}

func runJobCases(t *testing.T, cases []jobCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			job := validTestJob()
			if testCase.mutate != nil {
				testCase.mutate(&job)
			}
			err := ValidateJob(job)
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("ValidateJob unexpected error: %v", err)
				}
				return
			}
			assertCategory(t, err, testCase.want)
		})
	}
}

func TestValidateJob(t *testing.T) {
	runJobCases(t, []jobCase{
		{name: "valid", want: ""},
		{name: "empty timestamped transcript", mutate: func(job *TranscriptJob) { job.TimestampedTranscript = "" }},
		{
			name:   "wrong schema version",
			mutate: func(job *TranscriptJob) { job.SchemaVersion = 2 },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture key uppercase",
			mutate: func(job *TranscriptJob) { job.LectureKey = "EECS484/2026-fall/001" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture key malformed",
			mutate: func(job *TranscriptJob) { job.LectureKey = "eecs484/2026-fall/1" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture key above 128 characters",
			mutate: func(job *TranscriptJob) { job.LectureKey = strings.Repeat("a", 122) + "/2026-fall/001" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture key inconsistent with identity",
			mutate: func(job *TranscriptJob) { job.LectureKey = "eecs484/2026-fall/002" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "course slug uppercase",
			mutate: func(job *TranscriptJob) { job.CourseSlug = "EECS484" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "course slug above 64 characters",
			mutate: func(job *TranscriptJob) { job.CourseSlug = strings.Repeat("a", 65) },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "empty course name",
			mutate: func(job *TranscriptJob) { job.CourseName = "" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "course name with newline",
			mutate: func(job *TranscriptJob) { job.CourseName = "EECS\n484" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "course name above 256 characters",
			mutate: func(job *TranscriptJob) { job.CourseName = strings.Repeat("c", 257) },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "human term form",
			mutate: func(job *TranscriptJob) { job.Term = "Fall 2026" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "combined spring summer term",
			mutate: func(job *TranscriptJob) { job.Term = "2026-spring-summer" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture number zero",
			mutate: func(job *TranscriptJob) { job.LectureNumber = 0 },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture number above maximum",
			mutate: func(job *TranscriptJob) { job.LectureNumber = 1000 },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture date with impossible day",
			mutate: func(job *TranscriptJob) { job.LectureDate = "2026-02-30" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture date with impossible month",
			mutate: func(job *TranscriptJob) { job.LectureDate = "2026-13-01" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture date non leap year",
			mutate: func(job *TranscriptJob) { job.LectureDate = "2025-02-29" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "lecture date wrong format",
			mutate: func(job *TranscriptJob) { job.LectureDate = "09/01/2026" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "captured at with milliseconds",
			mutate: func(job *TranscriptJob) { job.CapturedAt = "2026-09-20T12:34:56.123Z" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "captured at with offset",
			mutate: func(job *TranscriptJob) { job.CapturedAt = "2026-09-20T12:34:56+00:00" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "captured at invalid hour",
			mutate: func(job *TranscriptJob) { job.CapturedAt = "2026-09-20T24:00:00Z" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "captured at invalid second",
			mutate: func(job *TranscriptJob) { job.CapturedAt = "2026-09-20T12:34:60Z" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "insecure source url",
			mutate: func(job *TranscriptJob) { job.SourceURL = "http://leccap.engin.umich.edu/lecture/123" },
			want:   ErrorRejectedUnsafeURL,
		},
		{
			name:   "foreign source url host",
			mutate: func(job *TranscriptJob) { job.SourceURL = "https://example.com/lecture/123" },
			want:   ErrorRejectedUnsafeURL,
		},
		{
			name:   "source url with userinfo",
			mutate: func(job *TranscriptJob) { job.SourceURL = "https://user:pass@leccap.engin.umich.edu/a" },
			want:   ErrorRejectedUnsafeURL,
		},
		{
			name:   "source url with non default port",
			mutate: func(job *TranscriptJob) { job.SourceURL = "https://leccap.engin.umich.edu:444/a" },
			want:   ErrorRejectedUnsafeURL,
		},
		{
			name:   "empty source url",
			mutate: func(job *TranscriptJob) { job.SourceURL = "" },
			want:   ErrorRejectedUnsafeURL,
		},
		{
			name:   "relative source url",
			mutate: func(job *TranscriptJob) { job.SourceURL = "/lecture/123" },
			want:   ErrorRejectedUnsafeURL,
		},
		{
			name:   "source url with CRLF",
			mutate: func(job *TranscriptJob) { job.SourceURL = "https://leccap.engin.umich.edu/a\r\nb" },
			want:   ErrorRejectedUnsafeURL,
		},
		{
			name:   "transcript below minimum content",
			mutate: func(job *TranscriptJob) { job.Transcript = strings.Repeat("a", 49) },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "transcript only whitespace",
			mutate: func(job *TranscriptJob) { job.Transcript = strings.Repeat(" ", 100) },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "transcript invalid utf8",
			mutate: func(job *TranscriptJob) { job.Transcript = testTranscript() + "\xff" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "transcript above byte limit",
			mutate: func(job *TranscriptJob) { job.Transcript = strings.Repeat("a", MaxTranscriptBytes+1) },
			want:   ErrorRejectedOversized,
		},
		{
			name:   "timestamped transcript invalid utf8",
			mutate: func(job *TranscriptJob) { job.TimestampedTranscript = testTranscript() + "\xff" },
			want:   ErrorRejectedInvalidSchema,
		},
		{
			name:   "timestamped transcript above byte limit",
			mutate: func(job *TranscriptJob) { job.TimestampedTranscript = strings.Repeat("a", MaxTranscriptBytes+1) },
			want:   ErrorRejectedOversized,
		},
		{
			name:   "uppercase content hash",
			mutate: func(job *TranscriptJob) { job.ContentHash = strings.Repeat("A", 64) },
			want:   ErrorRejectedInvalidHash,
		},
		{
			name:   "short content hash",
			mutate: func(job *TranscriptJob) { job.ContentHash = strings.Repeat("a", 63) },
			want:   ErrorRejectedInvalidHash,
		},
		{
			name:   "empty content hash",
			mutate: func(job *TranscriptJob) { job.ContentHash = "" },
			want:   ErrorRejectedInvalidHash,
		},
	})
}

func TestValidateJobByteBoundaries(t *testing.T) {
	job := validTestJob()
	job.Transcript = strings.Repeat("a", MaxTranscriptBytes)
	if err := ValidateJob(job); err != nil {
		t.Fatalf("transcript at exact byte limit must be valid: %v", err)
	}

	job = validTestJob()
	job.TimestampedTranscript = strings.Repeat("b", MaxTranscriptBytes)
	if err := ValidateJob(job); err != nil {
		t.Fatalf("timestamped transcript at exact byte limit must be valid: %v", err)
	}

	job = validTestJob()
	job.Transcript = strings.Repeat("\"", MaxTranscriptBytes)
	job.TimestampedTranscript = strings.Repeat("\"", MaxTranscriptBytes)
	assertCategory(t, ValidateJob(job), ErrorRejectedOversized)
}

func TestValidateJobErrorIsCategoryOnly(t *testing.T) {
	job := validTestJob()
	job.Transcript = "SECRET TRANSCRIPT TEXT " + testTranscript()
	job.ContentHash = "NOT-A-HASH"
	err := ValidateJob(job)
	assertCategory(t, err, ErrorRejectedInvalidHash)
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error %q leaked transcript content", err.Error())
	}
}

type sourceURLVector struct {
	ID                       string  `json:"id"`
	Input                    string  `json:"input"`
	ExpectedCanonicalURL     *string `json:"expectedCanonicalUrl"`
	ExpectedPublishSourceURL *string `json:"expectedPublishSourceUrl"`
	ExpectedAction           string  `json:"expectedAction"`
	ExpectedError            string  `json:"expectedError"`
}

func TestCanonicalizeSourceURLVectors(t *testing.T) {
	var vectors []sourceURLVector
	if err := json.Unmarshal(readProtocolFile(t, "source-url-vectors.json"), &vectors); err != nil {
		t.Fatalf("parse source-url-vectors.json: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("source-url-vectors.json is empty")
	}
	for _, vector := range vectors {
		t.Run(vector.ID, func(t *testing.T) {
			canonical, err := CanonicalizeSourceURL(vector.Input)
			switch vector.ExpectedAction {
			case "reject":
				if err == nil {
					t.Fatalf("CanonicalizeSourceURL(%q) = %q, want rejection", vector.Input, canonical)
				}
				if vector.ExpectedError != "" && vector.ExpectedError != string(ErrorRejectedUnsafeURL) {
					t.Fatalf("unexpected vector error expectation %q", vector.ExpectedError)
				}
				job := validTestJob()
				job.SourceURL = vector.Input
				assertCategory(t, ValidateJob(job), ErrorRejectedUnsafeURL)
			case "include", "omit":
				if err != nil {
					t.Fatalf("CanonicalizeSourceURL(%q): %v", vector.Input, err)
				}
				if vector.ExpectedCanonicalURL == nil || canonical != *vector.ExpectedCanonicalURL {
					t.Fatalf("CanonicalizeSourceURL(%q) = %q, want %v", vector.Input, canonical, vector.ExpectedCanonicalURL)
				}
				job := validTestJob()
				job.SourceURL = vector.Input
				canonicalJob, err := CanonicalizeJob(job)
				if err != nil {
					t.Fatalf("CanonicalizeJob(%q): %v", vector.Input, err)
				}
				if canonicalJob.SourceURL != canonical {
					t.Fatalf("CanonicalizeJob source = %q, want %q", canonicalJob.SourceURL, canonical)
				}
			default:
				t.Fatalf("unknown vector action %q", vector.ExpectedAction)
			}
		})
	}
}

func TestCanonicalizeSourceURLRules(t *testing.T) {
	valid := map[string]string{
		"HTTPS://LECCAP.ENGIN.UMICH.EDU:443/a/B/?q=x#fragment": "https://leccap.engin.umich.edu/a/B/",
		"https://leccap.engin.umich.edu":                       "https://leccap.engin.umich.edu/",
		"https://leccap.engin.umich.edu:443":                   "https://leccap.engin.umich.edu/",
		"https://leccap.engin.umich.edu/a%20b/":                "https://leccap.engin.umich.edu/a%20b/",
	}
	for input, expected := range valid {
		canonical, err := CanonicalizeSourceURL(input)
		if err != nil || canonical != expected {
			t.Fatalf("CanonicalizeSourceURL(%q) = %q, %v; want %q", input, canonical, err, expected)
		}
	}

	invalid := []string{
		"http://leccap.engin.umich.edu/a",
		"https://leccap.engin.umich.edu:444/a",
		"https://user:pass@leccap.engin.umich.edu/a",
		"https://example.com/a",
		"https://leccap.engin.umich.edu.evil.example/a",
		"//leccap.engin.umich.edu/a",
		"/a",
		"not a url",
		"",
		"https://leccap.engin.umich.edu/" + strings.Repeat("a", 3000),
		"https://leccap.engin.umich.edu/a\r\nb",
		"https://leccap.engin.umich.edu/\xff",
	}
	for _, input := range invalid {
		if canonical, err := CanonicalizeSourceURL(input); err == nil {
			t.Fatalf("CanonicalizeSourceURL(%q) = %q, want error", input, canonical)
		}
	}
}

func TestCanonicalizeJobNormalizesSafeURL(t *testing.T) {
	job := validTestJob()
	job.SourceURL = "https://LECCAP.ENGIN.UMICH.EDU:443/lecture/123?session=secret#transcript"
	canonical, err := CanonicalizeJob(job)
	if err != nil {
		t.Fatalf("CanonicalizeJob: %v", err)
	}
	if canonical.SourceURL != "https://leccap.engin.umich.edu/lecture/123" {
		t.Fatalf("canonical source = %q", canonical.SourceURL)
	}
	if err := ValidateJob(canonical); err != nil {
		t.Fatalf("canonical job must validate: %v", err)
	}
}

func TestMarshalCanonical(t *testing.T) {
	job := validTestJob()
	job.Transcript = strings.Repeat("Q&A <tag> & more ", 10)
	serialized, err := job.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	if !json.Valid(serialized) {
		t.Fatalf("MarshalCanonical produced invalid JSON: %s", serialized)
	}
	if bytes.HasSuffix(serialized, []byte("\n")) {
		t.Fatal("MarshalCanonical must not append a trailing newline")
	}
	for _, escaped := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if bytes.Contains(serialized, []byte(escaped)) {
			t.Fatalf("MarshalCanonical HTML-escaped %s: %s", escaped, serialized)
		}
	}
	if !bytes.Contains(serialized, []byte("Q&A <tag> & more")) {
		t.Fatalf("MarshalCanonical altered text: %s", serialized)
	}
	if bytes.Contains(serialized, []byte("\n")) || bytes.Contains(serialized, []byte(": ")) {
		t.Fatalf("MarshalCanonical must be compact: %s", serialized)
	}

	fieldOrder := []string{
		"schemaVersion", "lectureKey", "courseSlug", "courseName", "term",
		"lectureNumber", "lectureDate", "sourceUrl", "capturedAt", "transcript",
		"timestampedTranscript", "contentHash",
	}
	position := -1
	for _, field := range fieldOrder {
		index := bytes.Index(serialized, []byte(`"`+field+`":`))
		if index <= position {
			t.Fatalf("field %s out of canonical order in %s", field, serialized)
		}
		position = index
	}
}

func TestCalendarAndTimestampHelpers(t *testing.T) {
	validDates := []string{"2026-09-01", "2024-02-29", "2000-02-29", "1999-12-31"}
	for _, value := range validDates {
		if !validCalendarDate(value) {
			t.Fatalf("validCalendarDate(%q) = false", value)
		}
	}
	invalidDates := []string{"2025-02-29", "1900-02-29", "2026-04-31", "2026-00-10", "2026-01-00", "2026-1-01", "26-01-01", ""}
	for _, value := range invalidDates {
		if validCalendarDate(value) {
			t.Fatalf("validCalendarDate(%q) = true", value)
		}
	}

	validTimestamps := []string{"2026-09-20T00:00:00Z", "2026-09-20T23:59:59Z"}
	for _, value := range validTimestamps {
		if !validUTCSecond(value) {
			t.Fatalf("validUTCSecond(%q) = false", value)
		}
	}
	invalidTimestamps := []string{
		"2026-09-20T24:00:00Z", "2026-09-20T12:60:00Z", "2026-09-20T12:00:60Z",
		"2026-09-20T12:00:00.000Z", "2026-09-20T12:00:00+00:00", "2026-09-20 12:00:00Z", "",
	}
	for _, value := range invalidTimestamps {
		if validUTCSecond(value) {
			t.Fatalf("validUTCSecond(%q) = true", value)
		}
	}
}

func TestZeroPadLecture(t *testing.T) {
	cases := map[int]string{1: "001", 6: "006", 42: "042", 999: "999", 0: "000", 1000: ""}
	for number, expected := range cases {
		if got := zeroPadLecture(number); got != expected {
			t.Fatalf("zeroPadLecture(%d) = %q, want %q", number, got, expected)
		}
	}
}

func TestIsValidHelpers(t *testing.T) {
	if !IsValidLectureKey("eecs484/2026-fall/001") {
		t.Fatal("expected valid lecture key")
	}
	if IsValidLectureKey("eecs484/2026-fall/0000") || IsValidLectureKey("") || IsValidLectureKey(strings.Repeat("a", 129)) {
		t.Fatal("expected invalid lecture key")
	}
	if !IsValidContentHash(strings.Repeat("a", 64)) {
		t.Fatal("expected valid content hash")
	}
	if IsValidContentHash(strings.Repeat("A", 64)) || IsValidContentHash(strings.Repeat("a", 63)) {
		t.Fatal("expected invalid content hash")
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

// frameTranscriptHashInput mirrors the normative transcript-hash-v1 framing.
// It is deliberately test-local: the protocol package has no Go normalizer, so
// this helper can only be applied to forms that are already normalized (the
// TypeScript fixture).  Cross-language normalization parity is therefore
// covered by the shared vectors in TypeScript only.
func frameTranscriptHashInput(transcript, timestampedTranscript string) []byte {
	var buffer bytes.Buffer
	buffer.WriteString("transcript-hash-v1\x00")
	fmt.Fprintf(&buffer, "%d:", len([]byte(transcript)))
	buffer.WriteString(transcript)
	fmt.Fprintf(&buffer, "%d:", len([]byte(timestampedTranscript)))
	buffer.WriteString(timestampedTranscript)
	return buffer.Bytes()
}

// TestCanonicalJobFixtureHashFraming proves Go reproduces the TypeScript hash
// for the already-normalized fixture bytes.  It does not test normalization,
// which has no Go implementation in the protocol package.
func TestCanonicalJobFixtureHashFraming(t *testing.T) {
	fixture := readTestdata(t, "transcript-job.canonical.json")
	job, err := decodeJob(fixture)
	if err != nil {
		t.Fatalf("decodeJob(fixture): %v", err)
	}
	sum := sha256.Sum256(frameTranscriptHashInput(job.Transcript, job.TimestampedTranscript))
	if got := hex.EncodeToString(sum[:]); got != job.ContentHash {
		t.Fatalf("Go hash framing = %s, TypeScript fixture hash = %s", got, job.ContentHash)
	}
}

// TestCanonicalJobFixtureFromTypeScript consumes the cross-language fixture
// produced by extension/src/transcript-job.ts.  Byte-for-byte equality proves
// both languages agree on field order, escaping, and UTF-8 byte length.
func TestCanonicalJobFixtureFromTypeScript(t *testing.T) {
	fixture := readTestdata(t, "transcript-job.canonical.json")
	if len(fixture) == 0 {
		t.Fatal("fixture is empty")
	}
	if fixture[len(fixture)-1] == '\n' {
		t.Fatal("fixture must be compact JSON with no trailing newline")
	}
	if len(fixture) > MaxSerializedJobByte {
		t.Fatalf("fixture is %d bytes, above the %d byte serialized job limit", len(fixture), MaxSerializedJobByte)
	}
	if !strings.Contains(string(fixture), "&") || !strings.Contains(string(fixture), "<") {
		t.Fatal("fixture must exercise raw ampersand/angle-bracket escaping parity")
	}

	job, err := decodeJob(fixture)
	if err != nil {
		t.Fatalf("decodeJob(fixture): %v", err)
	}
	if err := ValidateJob(job); err != nil {
		t.Fatalf("ValidateJob(fixture): %v", err)
	}

	serialized, err := job.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	if !bytes.Equal(serialized, fixture) {
		t.Fatalf("Go canonical bytes differ from the TypeScript fixture:\n got %s\nwant %s", serialized, fixture)
	}
	if len(serialized) != len(fixture) {
		t.Fatalf("compact byte length = %d, want %d", len(serialized), len(fixture))
	}

	envelope := `{"type":"submit_job","protocolVersion":1,"requestId":"fixture-1","job":` + string(fixture) + `}`
	request, err := DecodeRequest([]byte(envelope))
	if err != nil {
		t.Fatalf("DecodeRequest(submit fixture): %v", err)
	}
	if request.Job == nil || !reflect.DeepEqual(*request.Job, job) {
		t.Fatalf("submit fixture decoded a different job: %+v", request.Job)
	}
}
