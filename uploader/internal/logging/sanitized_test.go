package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func openLogger(t *testing.T) (*Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Logs", "uploader.log")
	logger, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { logger.Close() })
	return logger, path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestOpenCreatesRestrictedLogPath(t *testing.T) {
	logger, path := openLogger(t)
	if logger.Path() != path {
		t.Fatalf("Path = %q, want %q", logger.Path(), path)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat log dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("log dir mode = %#o, want 0700", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("log file mode = %#o, want 0600", fileInfo.Mode().Perm())
	}
	if _, err := Open(""); err == nil {
		t.Fatal("empty log path must fail")
	}
}

func TestLogsStructuredAllowedFields(t *testing.T) {
	logger, path := openLogger(t)
	logger.now = func() time.Time { return testTime }
	logger.Info(EventJobClaimed, Fields{
		JobID:         7,
		LectureKey:    "eecs491/2026-winter/006",
		CourseSlug:    "eecs491",
		Term:          "2026-winter",
		LectureNumber: 6,
		Status:        "uploading",
		ErrorCategory: "internal",
		SourceURL:     "https://leccap.engin.umich.edu/lecture/123?token=abc#frag",
		Attempt:       2,
		HTTPStatus:    503,
		Count:         3,
	})
	logger.Warn(EventAuthState, Fields{Status: "authorizing"})
	logger.Error(EventProtocolError, Fields{ErrorCategory: "invalid_message"})
	logger.Close()

	lines := strings.Split(strings.TrimSpace(readFile(t, path)), "\n")
	if len(lines) != 3 {
		t.Fatalf("log lines = %d, want 3", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line is not JSON: %v", err)
	}
	expected := map[string]any{
		"time":          "2026-09-20T12:00:00Z",
		"level":         "info",
		"event":         "job_claimed",
		"jobId":         float64(7),
		"lectureKey":    "eecs491/2026-winter/006",
		"courseSlug":    "eecs491",
		"term":          "2026-winter",
		"lectureNumber": float64(6),
		"status":        "uploading",
		"errorCategory": "internal",
		"source":        "leccap.engin.umich.edu/lecture/123",
		"attempt":       float64(2),
		"httpStatus":    float64(503),
		"count":         float64(3),
	}
	for key, want := range expected {
		if got := first[key]; got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
	if strings.Contains(readFile(t, path), "token=abc") || strings.Contains(readFile(t, path), "#frag") {
		t.Fatal("query and fragment must never be logged")
	}
}

func TestRedactsSensitiveSamples(t *testing.T) {
	logger, path := openLogger(t)
	logger.Info(Event("transcript_body"), Fields{
		LectureKey:    "transcript_body=hello lecture text",
		CourseSlug:    "token=ghp_secret_value",
		Term:          "sessionid",
		Status:        "WDJB-MJHT",
		ErrorCategory: "user_code=WDJB-MJHT",
		SourceURL:     "https://leccap.engin.umich.edu/lecture/123?token=abc&sessionid=def#fragment",
		LectureNumber: 6,
		Attempt:       1,
		HTTPStatus:    200,
		Count:         2,
	})
	logger.Close()

	logged := readFile(t, path)
	for _, sample := range []string{
		"transcript_body",
		"token=",
		"token:",
		"sessionid",
		"session_id",
		"WDJB-MJHT",
		"user_code",
		"device_code",
		"?",
		"#",
		"ghp_secret_value",
		"abc",
		"def",
		"hello lecture text",
	} {
		if strings.Contains(logged, sample) {
			t.Fatalf("log leaked sensitive sample %q:\n%s", sample, logged)
		}
	}
	if !strings.Contains(logged, "leccap.engin.umich.edu/lecture/123") {
		t.Fatalf("log must keep host and path:\n%s", logged)
	}
	if !strings.Contains(logged, Redacted) {
		t.Fatalf("log must mark redacted values:\n%s", logged)
	}
	if !strings.Contains(logged, `"event":"unknown"`) {
		t.Fatalf("sensitive event names must be redacted:\n%s", logged)
	}
}

func TestSanitizeSource(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"query and fragment", "https://leccap.engin.umich.edu/lecture/123?token=abc&sessionid=def#frag", "leccap.engin.umich.edu/lecture/123"},
		{"lowercase host", "https://LECCAP.ENGIN.UMICH.EDU/lecture/123", "leccap.engin.umich.edu/lecture/123"},
		{"empty path", "https://leccap.engin.umich.edu", "leccap.engin.umich.edu/"},
		{"http", "http://leccap.engin.umich.edu/lecture/123", "leccap.engin.umich.edu/lecture/123"},
		{"userinfo", "https://user:pass@leccap.engin.umich.edu/lecture/123", ""},
		{"other scheme", "ftp://leccap.engin.umich.edu/lecture", ""},
		{"not a url", "not a url", ""},
		{"empty", "", ""},
		{"sensitive path segment", "https://leccap.engin.umich.edu/lecture/sessionid/123", Redacted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeSource(tc.input); got != tc.want {
				t.Fatalf("sanitizeSource(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestRotationKeepsAtMostThreeRestrictedFiles(t *testing.T) {
	logger, path := openLogger(t)
	logger.now = func() time.Time { return testTime }
	logger.maxBytes = 300
	logger.maxFiles = 3
	for i := 1; i <= 30; i++ {
		logger.Info(EventJobEnqueued, Fields{
			JobID:      int64(i),
			LectureKey: "eecs491/2026-winter/006",
			Status:     "queued",
		})
	}
	logger.Close()

	matches, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("rotated files = %v, want exactly 3", matches)
	}
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			t.Fatalf("stat %s: %v", match, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %#o, want 0600", match, info.Mode().Perm())
		}
		if info.Size() > 300 {
			t.Fatalf("%s size = %d, want <= 300", match, info.Size())
		}
	}
	current := readFile(t, path)
	previous := readFile(t, path+".1")
	if !strings.Contains(current, `"jobId":30`) && !strings.Contains(previous, `"jobId":30`) {
		t.Fatalf("latest entry lost across rotation:\ncurrent:\n%s\nprevious:\n%s", current, previous)
	}
}

func TestCloseIsIdempotentAndSafe(t *testing.T) {
	logger, path := openLogger(t)
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	logger.Info(EventStartup, Fields{})
	var nilLogger *Logger
	nilLogger.Info(EventStartup, Fields{})
	if err := nilLogger.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
	if nilLogger.Path() != "" {
		t.Fatal("nil Path must be empty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file must remain on disk: %v", err)
	}
}
