package logging

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

const Redacted = "[redacted]"

type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

type Event string

const (
	EventStartup              Event = "startup"
	EventShutdown             Event = "shutdown"
	EventConfigLoaded         Event = "config_loaded"
	EventLockAcquired         Event = "lock_acquired"
	EventLockAlreadyRunning   Event = "lock_already_running"
	EventQueueOpened          Event = "queue_opened"
	EventJobEnqueued          Event = "job_enqueued"
	EventJobDuplicate         Event = "job_duplicate"
	EventJobQueueFull         Event = "job_queue_full"
	EventJobClaimed           Event = "job_claimed"
	EventJobUploaded          Event = "job_uploaded"
	EventJobUnchanged         Event = "job_unchanged"
	EventJobRetryableError    Event = "job_retryable_error"
	EventJobPermanentConflict Event = "job_permanent_conflict"
	EventJobRejected          Event = "job_rejected"
	EventRetryPromoted        Event = "retry_promoted"
	EventLeaseRecovered       Event = "lease_recovered"
	EventJobsPruned           Event = "jobs_pruned"
	EventAuthState            Event = "auth_state"
	EventGitHubRequest        Event = "github_request"
	EventProtocolError        Event = "protocol_error"
)

type Fields struct {
	JobID         int64
	LectureKey    string
	CourseSlug    string
	Term          string
	LectureNumber int
	Status        string
	ErrorCategory string
	SourceURL     string
	Attempt       int
	HTTPStatus    int
	Count         int
}

type Logger struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	size     int64
	maxBytes int64
	maxFiles int
	now      func() time.Time
}

type entry struct {
	Time          string `json:"time"`
	Level         Level  `json:"level"`
	Event         string `json:"event"`
	JobID         int64  `json:"jobId,omitempty"`
	LectureKey    string `json:"lectureKey,omitempty"`
	CourseSlug    string `json:"courseSlug,omitempty"`
	Term          string `json:"term,omitempty"`
	LectureNumber int    `json:"lectureNumber,omitempty"`
	Status        string `json:"status,omitempty"`
	ErrorCategory string `json:"errorCategory,omitempty"`
	Attempt       int    `json:"attempt,omitempty"`
	HTTPStatus    int    `json:"httpStatus,omitempty"`
	Count         int    `json:"count,omitempty"`
	Source        string `json:"source,omitempty"`
}

func Open(path string) (*Logger, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("log path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict log directory: %w", err)
	}
	logger := &Logger{
		path:     path,
		maxBytes: config.LogMaxBytes,
		maxFiles: config.LogMaxFiles,
		now:      time.Now,
	}
	if err := logger.openFile(); err != nil {
		return nil, err
	}
	return logger, nil
}

func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *Logger) Info(event Event, fields Fields)  { l.log(LevelInfo, event, fields) }
func (l *Logger) Warn(event Event, fields Fields)  { l.log(LevelWarn, event, fields) }
func (l *Logger) Error(event Event, fields Fields) { l.log(LevelError, event, fields) }

func (l *Logger) log(level Level, event Event, fields Fields) {
	if l == nil {
		return
	}
	line := l.encode(level, event, fields)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	if err := l.rotateLocked(int64(len(line))); err != nil {
		return
	}
	written, err := l.file.Write(line)
	if err == nil {
		l.size += int64(written)
	}
}

func (l *Logger) encode(level Level, event Event, fields Fields) []byte {
	value := entry{
		Time:          l.now().UTC().Truncate(time.Second).Format(time.RFC3339),
		Level:         level,
		Event:         sanitizeEvent(event),
		JobID:         fields.JobID,
		LectureKey:    sanitizeField(fields.LectureKey, validLectureKey),
		CourseSlug:    sanitizeField(fields.CourseSlug, validCourseSlug),
		Term:          sanitizeField(fields.Term, validTerm),
		LectureNumber: sanitizeLectureNumber(fields.LectureNumber),
		Status:        sanitizeField(fields.Status, validStatus),
		ErrorCategory: sanitizeField(fields.ErrorCategory, validErrorCategory),
		Attempt:       nonNegative(fields.Attempt),
		HTTPStatus:    sanitizeHTTPStatus(fields.HTTPStatus),
		Count:         nonNegative(fields.Count),
		Source:        sanitizeSource(fields.SourceURL),
	}
	line, err := json.Marshal(value)
	if err != nil {
		return []byte("{\"level\":\"error\",\"event\":\"unknown\"}\n")
	}
	return append(line, '\n')
}

func (l *Logger) openFile() error {
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open uploader log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("restrict uploader log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("inspect uploader log: %w", err)
	}
	l.file = file
	l.size = info.Size()
	return nil
}

func (l *Logger) rotateLocked(pending int64) error {
	if l.size == 0 || l.size+pending <= l.maxBytes || l.maxFiles < 2 {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	oldest := fmt.Sprintf("%s.%d", l.path, l.maxFiles-1)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := l.maxFiles - 2; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", l.path, i)
		to := fmt.Sprintf("%s.%d", l.path, i+1)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return l.openFile()
}

var (
	courseSlugPattern = regexp.MustCompile(`^[a-z0-9]{1,64}$`)
	termPattern       = regexp.MustCompile(`^[0-9]{4}-(winter|spring|summer|fall)$`)
	eventPattern      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	sensitiveMarkers  = []string{
		"transcript_body",
		"transcript=",
		"token=",
		"token:",
		"access_token",
		"refresh_token",
		"sessionid",
		"session_id",
		"cookie",
		"authorization",
		"bearer ",
		"device_code",
		"user_code",
	}
)

func sanitizeField(value string, valid func(string) bool) string {
	if value == "" {
		return ""
	}
	if !valid(value) || containsSensitive(value) {
		return Redacted
	}
	return value
}

func sanitizeEvent(event Event) string {
	value := string(event)
	if !eventPattern.MatchString(value) || containsSensitive(value) {
		return "unknown"
	}
	return value
}

func sanitizeLectureNumber(value int) int {
	if value < 1 || value > 999 {
		return 0
	}
	return value
}

func sanitizeHTTPStatus(value int) int {
	if value < 100 || value > 599 {
		return 0
	}
	return value
}

func nonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func validLectureKey(value string) bool {
	return protocol.IsValidLectureKey(value)
}

func validCourseSlug(value string) bool {
	return courseSlugPattern.MatchString(value)
}

func validTerm(value string) bool {
	return termPattern.MatchString(value)
}

func validStatus(value string) bool {
	return protocol.IsQueueStatus(protocol.QueueStatus(value)) ||
		protocol.IsAuthState(protocol.AuthState(value)) ||
		protocol.IsDrainState(protocol.DrainState(value))
}

func validErrorCategory(value string) bool {
	return len(value) <= 64 && protocol.IsErrorCategory(protocol.ErrorCategory(value))
}

func sanitizeSource(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	source := host + path
	if containsSensitive(source) {
		return Redacted
	}
	return source
}

func containsSensitive(value string) bool {
	lowered := strings.ToLower(value)
	for _, marker := range sensitiveMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}
