package queue

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

var (
	ErrAlreadyRunning    = errors.New("already_running")
	ErrJobNotFound       = errors.New("job not found")
	ErrNotEligible       = errors.New("job is not eligible")
	ErrUnsupportedSchema = errors.New("unsupported queue schema version")
)

type EnqueueOutcome int

const (
	EnqueueQueued EnqueueOutcome = iota
	EnqueueDuplicate
	EnqueueDuplicateTerminal
	EnqueueRejectedQueueFull
)

type EnqueueResult struct {
	Outcome        EnqueueOutcome
	JobID          int64
	ExistingStatus protocol.QueueStatus
	Action         *protocol.DuplicateAction
}

type Job struct {
	ID                  int64
	LectureKey          string
	ContentHash         string
	Payload             protocol.TranscriptJob
	Status              protocol.QueueStatus
	AttemptCount        int
	NextAttemptAt       *time.Time
	LeaseStartedAt      *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastErrorCategory   *string
	LastErrorHTTPStatus *int
	RemoteContentHash   *string
	RemoteFileKind      *string
}

type Store struct {
	db   *sql.DB
	path string
}

// TargetPath is the single write-once plain-transcript path: the plain
// transcript lives directly under the course slug. The term stays part of the
// lecture identity and lectureKey but not of the repository path.
func TargetPath(courseSlug string, lectureNumber int) string {
	return fmt.Sprintf("%s/%03d.md", courseSlug, lectureNumber)
}

// TimestampedPath is the write-once timestamped-transcript path.
func TimestampedPath(courseSlug string, lectureNumber int) string {
	return fmt.Sprintf("%s/timestamped/%03d.md", courseSlug, lectureNumber)
}

func (j Job) TargetPath() string {
	return TargetPath(j.Payload.CourseSlug, j.Payload.LectureNumber)
}

func (j Job) TimestampedPath() string {
	return TimestampedPath(j.Payload.CourseSlug, j.Payload.LectureNumber)
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("queue path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create queue directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict queue directory: %w", err)
	}
	if err := ensureFile(path, 0o600); err != nil {
		return nil, err
	}

	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open queue database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open queue database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := secureFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Path() string { return s.path }

func ensureFile(path string, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, mode)
	if err != nil {
		return fmt.Errorf("create queue database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("create queue database: %w", err)
	}
	return os.Chmod(path, mode)
}

func secureFiles(path string) error {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("restrict queue file: %w", err)
		}
	}
	return nil
}

func migrate(db *sql.DB) error {
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&exists); err != nil {
		return fmt.Errorf("inspect queue schema: %w", err)
	}
	if exists == 0 {
		if _, err := db.Exec(schemaSQL); err != nil {
			return fmt.Errorf("apply queue schema: %w", err)
		}
	}
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return fmt.Errorf("read queue schema version: %w", err)
	}
	if version != 1 {
		return fmt.Errorf("%w: %d", ErrUnsupportedSchema, version)
	}
	return nil
}

func (s *Store) Enqueue(job protocol.TranscriptJob, now time.Time) (EnqueueResult, error) {
	canonical, err := protocol.CanonicalizeJob(job)
	if err != nil {
		return EnqueueResult{}, err
	}
	payload, err := canonical.MarshalCanonical()
	if err != nil {
		return EnqueueResult{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return EnqueueResult{}, err
	}
	defer tx.Rollback()

	var existingID int64
	var existingStatus string
	err = tx.QueryRow(`SELECT id, status FROM jobs WHERE lecture_key = ? AND content_hash = ?`, canonical.LectureKey, canonical.ContentHash).Scan(&existingID, &existingStatus)
	if err == nil {
		return duplicateResult(existingID, protocol.QueueStatus(existingStatus)), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return EnqueueResult{}, err
	}

	jobs, bytes, err := usageTx(tx)
	if err != nil {
		return EnqueueResult{}, err
	}
	if wouldExceedLimits(jobs, bytes, int64(len(payload))) {
		return EnqueueResult{Outcome: EnqueueRejectedQueueFull}, nil
	}

	nowText := formatTime(now)
	result, err := tx.Exec(
		`INSERT INTO jobs (lecture_key, content_hash, job_json, status, attempt_count, created_at, updated_at) VALUES (?, ?, ?, ?, 0, ?, ?)`,
		canonical.LectureKey, canonical.ContentHash, string(payload), protocol.StatusQueued, nowText, nowText,
	)
	if err != nil {
		return EnqueueResult{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return EnqueueResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return EnqueueResult{}, err
	}
	return EnqueueResult{Outcome: EnqueueQueued, JobID: id, ExistingStatus: protocol.StatusQueued}, nil
}

func duplicateResult(id int64, status protocol.QueueStatus) EnqueueResult {
	result := EnqueueResult{Outcome: EnqueueDuplicate, JobID: id, ExistingStatus: status}
	if !isRejection(status) {
		return result
	}
	result.Outcome = EnqueueDuplicateTerminal
	action := protocol.ActionDiscardExistingThenRecapture
	if status == protocol.StatusRejectedPermission {
		action = protocol.ActionRetryExisting
	}
	result.Action = &action
	return result
}

func wouldExceedLimits(jobs int, bytes, incoming int64) bool {
	return jobs+1 > config.QueueMaxJobs || bytes+incoming > config.QueueMaxBytes
}

func (s *Store) ClaimNext(now time.Time) (*Job, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	nowText := formatTime(now)
	var id int64
	err = tx.QueryRow(
		`SELECT id FROM jobs WHERE status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?) ORDER BY created_at ASC, id ASC LIMIT 1`,
		protocol.StatusQueued, nowText,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(
		`UPDATE jobs SET status = ?, lease_started_at = ?, updated_at = ? WHERE id = ? AND status = ?`,
		protocol.StatusUploading, nowText, nowText, id, protocol.StatusQueued,
	)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, ErrNotEligible
	}
	job, err := scanJob(tx.QueryRow(jobSelect+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Store) MarkUploaded(id int64, now time.Time) error {
	if err := s.transition(
		`UPDATE jobs SET status = ?, next_attempt_at = NULL, lease_started_at = NULL, last_error_category = NULL, last_error_http_status = NULL, updated_at = ? WHERE id = ? AND status = ?`,
		protocol.StatusUploaded, formatTime(now), id, protocol.StatusUploading,
	); err != nil {
		return err
	}
	_, err := s.Prune(now)
	return err
}

func (s *Store) MarkUnchanged(id int64, remoteHash string, now time.Time) error {
	if !protocol.IsValidContentHash(remoteHash) {
		return errors.New("invalid remote content hash")
	}
	if err := s.transition(
		`UPDATE jobs SET status = ?, next_attempt_at = NULL, lease_started_at = NULL, last_error_category = NULL, last_error_http_status = NULL, remote_content_hash = ?, remote_file_kind = 'file', updated_at = ? WHERE id = ? AND status = ?`,
		protocol.StatusUnchanged, remoteHash, formatTime(now), id, protocol.StatusUploading,
	); err != nil {
		return err
	}
	_, err := s.Prune(now)
	return err
}

func (s *Store) MarkRetryableError(id int64, category string, httpStatus *int, nextAttemptAt time.Time, now time.Time) error {
	if err := validateErrorMeta(category, httpStatus); err != nil {
		return err
	}
	if nextAttemptAt.IsZero() {
		return errors.New("next attempt time is required")
	}
	return s.transition(
		`UPDATE jobs SET status = ?, attempt_count = attempt_count + 1, next_attempt_at = ?, lease_started_at = NULL, last_error_category = ?, last_error_http_status = ?, updated_at = ? WHERE id = ? AND status = ?`,
		protocol.StatusRetryableError, formatTime(nextAttemptAt), category, intArg(httpStatus), formatTime(now), id, protocol.StatusUploading,
	)
}

func (s *Store) MarkPermanentConflict(id int64, category string, httpStatus *int, remoteHash *string, remoteKind string, now time.Time) error {
	if err := validateErrorMeta(category, httpStatus); err != nil {
		return err
	}
	if !protocol.IsRemoteFileKind(protocol.RemoteFileKind(remoteKind)) {
		return errors.New("invalid remote file kind")
	}
	if remoteHash != nil && !protocol.IsValidContentHash(*remoteHash) {
		return errors.New("invalid remote content hash")
	}
	return s.transition(
		`UPDATE jobs SET status = ?, attempt_count = attempt_count + 1, next_attempt_at = NULL, lease_started_at = NULL, last_error_category = ?, last_error_http_status = ?, remote_content_hash = ?, remote_file_kind = ?, updated_at = ? WHERE id = ? AND status = ?`,
		protocol.StatusPermanentConflict, category, intArg(httpStatus), stringArg(remoteHash), remoteKind, formatTime(now), id, protocol.StatusUploading,
	)
}

func (s *Store) MarkRejectedPermission(id int64, httpStatus *int, now time.Time) error {
	return s.MarkRejected(id, protocol.StatusRejectedPermission, httpStatus, now)
}

func (s *Store) MarkRejected(id int64, status protocol.QueueStatus, httpStatus *int, now time.Time) error {
	if !isRejection(status) {
		return errors.New("status is not a rejection")
	}
	if err := validateErrorMeta(string(status), httpStatus); err != nil {
		return err
	}
	return s.transition(
		`UPDATE jobs SET status = ?, attempt_count = attempt_count + 1, next_attempt_at = NULL, lease_started_at = NULL, last_error_category = ?, last_error_http_status = ?, updated_at = ? WHERE id = ? AND status = ?`,
		status, string(status), intArg(httpStatus), formatTime(now), id, protocol.StatusUploading,
	)
}

func (s *Store) PromoteDueRetries(now time.Time) (int64, error) {
	result, err := s.db.Exec(
		`UPDATE jobs SET status = ?, next_attempt_at = NULL, updated_at = ? WHERE status = ? AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?`,
		protocol.StatusQueued, formatTime(now), protocol.StatusRetryableError, formatTime(now),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) RecoverStaleLeases(now time.Time, lease time.Duration) (int64, error) {
	if lease <= 0 {
		lease = config.UploadLease
	}
	result, err := s.db.Exec(
		`UPDATE jobs SET status = ?, lease_started_at = NULL, attempt_count = attempt_count + 1, updated_at = ? WHERE status = ? AND (lease_started_at IS NULL OR lease_started_at <= ?)`,
		protocol.StatusQueued, formatTime(now), protocol.StatusUploading, formatTime(now.Add(-lease)),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) RetryJob(id int64, now time.Time) (protocol.QueueStatus, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRow(`SELECT status FROM jobs WHERE id = ?`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrJobNotFound
		}
		return "", err
	}
	current := protocol.QueueStatus(status)
	switch current {
	case protocol.StatusRetryableError, protocol.StatusPermanentConflict, protocol.StatusRejectedPermission:
	default:
		return current, ErrNotEligible
	}
	if _, err := tx.Exec(
		`UPDATE jobs SET status = ?, next_attempt_at = NULL, lease_started_at = NULL, updated_at = ? WHERE id = ? AND status = ?`,
		protocol.StatusQueued, formatTime(now), id, current,
	); err != nil {
		return current, err
	}
	if err := tx.Commit(); err != nil {
		return current, err
	}
	return protocol.StatusQueued, nil
}

func (s *Store) DiscardJob(id int64, now time.Time) (protocol.QueueStatus, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRow(`SELECT status FROM jobs WHERE id = ?`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrJobNotFound
		}
		return "", err
	}
	current := protocol.QueueStatus(status)
	if current != protocol.StatusPermanentConflict && !isRejection(current) {
		return current, ErrNotEligible
	}
	if _, err := tx.Exec(`DELETE FROM jobs WHERE id = ?`, id); err != nil {
		return current, err
	}
	if err := tx.Commit(); err != nil {
		return current, err
	}
	return current, nil
}

func (s *Store) Prune(now time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	deleted, err := pruneTx(tx, now)
	if err != nil {
		return deleted, err
	}
	if err := tx.Commit(); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func pruneTx(tx *sql.Tx, now time.Time) (int64, error) {
	cutoff := formatTime(now.Add(-config.QueueRetention))
	var deleted int64
	for {
		jobs, bytes, err := usageTx(tx)
		if err != nil {
			return deleted, err
		}
		if jobs < config.QueueMaxJobs && bytes < config.QueueMaxBytes {
			return deleted, nil
		}
		var id int64
		err = tx.QueryRow(
			`SELECT id FROM jobs WHERE status IN (?, ?) AND updated_at < ? ORDER BY updated_at ASC, id ASC LIMIT 1`,
			protocol.StatusUploaded, protocol.StatusUnchanged, cutoff,
		).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return deleted, nil
		}
		if err != nil {
			return deleted, err
		}
		if _, err := tx.Exec(`DELETE FROM jobs WHERE id = ?`, id); err != nil {
			return deleted, err
		}
		deleted++
	}
}

func (s *Store) Get(id int64) (*Job, error) {
	job, err := scanJob(s.db.QueryRow(jobSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	return job, err
}

func (s *Store) Counts() (protocol.QueueCounts, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return protocol.QueueCounts{}, err
	}
	defer rows.Close()
	var counts protocol.QueueCounts
	for rows.Next() {
		var status string
		var value int
		if err := rows.Scan(&status, &value); err != nil {
			return protocol.QueueCounts{}, err
		}
		if err := setCount(&counts, protocol.QueueStatus(status), value); err != nil {
			return protocol.QueueCounts{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return protocol.QueueCounts{}, err
	}
	return counts, nil
}

func (s *Store) StatusPage(beforeJobID *int64, limit int) ([]protocol.JobSummary, *int64, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}
	query := jobSelect
	var args []any
	if beforeJobID != nil {
		query += ` WHERE id < ?`
		args = append(args, *beforeJobID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	summaries := make([]protocol.JobSummary, 0, limit)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, nil, err
		}
		summaries = append(summaries, job.Summary())
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(summaries) <= limit {
		return summaries, nil, nil
	}
	summaries = summaries[:limit]
	next := summaries[len(summaries)-1].JobID
	return summaries, &next, nil
}

func (s *Store) Usage() (int, int64, error) {
	var jobs int
	var bytes sql.NullInt64
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(CAST(job_json AS BLOB))), 0) FROM jobs`).Scan(&jobs, &bytes); err != nil {
		return 0, 0, err
	}
	return jobs, bytes.Int64, nil
}

func (j *Job) Summary() protocol.JobSummary {
	summary := protocol.JobSummary{
		JobID:               j.ID,
		LectureKey:          j.LectureKey,
		ContentHash:         j.ContentHash,
		Status:              j.Status,
		AttemptCount:        j.AttemptCount,
		TargetPath:          j.TargetPath(),
		LastErrorCategory:   j.LastErrorCategory,
		LastErrorHTTPStatus: j.LastErrorHTTPStatus,
		RemoteContentHash:   j.RemoteContentHash,
	}
	if j.NextAttemptAt != nil {
		value := formatTime(*j.NextAttemptAt)
		summary.NextAttemptAt = &value
	}
	if !j.UpdatedAt.IsZero() {
		value := formatTime(j.UpdatedAt)
		summary.UpdatedAt = &value
	}
	if j.RemoteFileKind != nil {
		kind := protocol.RemoteFileKind(*j.RemoteFileKind)
		summary.RemoteFileKind = &kind
	}
	return summary
}

const jobSelect = `SELECT id, lecture_key, content_hash, job_json, status, attempt_count, next_attempt_at, lease_started_at, created_at, updated_at, last_error_category, last_error_http_status, remote_content_hash, remote_file_kind FROM jobs`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*Job, error) {
	var (
		job                                         Job
		payload, status, createdAt, updatedAt       string
		nextAttemptAt, leaseStartedAt, lastCategory sql.NullString
		remoteHash, remoteKind                      sql.NullString
		lastHTTP                                    sql.NullInt64
	)
	err := row.Scan(
		&job.ID, &job.LectureKey, &job.ContentHash, &payload, &status, &job.AttemptCount,
		&nextAttemptAt, &leaseStartedAt, &createdAt, &updatedAt, &lastCategory, &lastHTTP,
		&remoteHash, &remoteKind,
	)
	if err != nil {
		return nil, err
	}
	job.Status = protocol.QueueStatus(status)
	if err := json.Unmarshal([]byte(payload), &job.Payload); err != nil {
		return nil, fmt.Errorf("decode stored job: %w", err)
	}
	if job.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if job.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	if job.NextAttemptAt, err = timePtr(nextAttemptAt); err != nil {
		return nil, err
	}
	if job.LeaseStartedAt, err = timePtr(leaseStartedAt); err != nil {
		return nil, err
	}
	if lastHTTP.Valid {
		value := int(lastHTTP.Int64)
		job.LastErrorHTTPStatus = &value
	}
	if lastCategory.Valid {
		value := lastCategory.String
		job.LastErrorCategory = &value
	}
	if remoteHash.Valid {
		value := remoteHash.String
		job.RemoteContentHash = &value
	}
	if remoteKind.Valid {
		value := remoteKind.String
		job.RemoteFileKind = &value
	}
	return &job, nil
}

func (s *Store) transition(query string, args ...any) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrNotEligible
	}
	return tx.Commit()
}

func usageTx(tx *sql.Tx) (int, int64, error) {
	var jobs int
	var bytes sql.NullInt64
	if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(CAST(job_json AS BLOB))), 0) FROM jobs`).Scan(&jobs, &bytes); err != nil {
		return 0, 0, err
	}
	return jobs, bytes.Int64, nil
}

func validateErrorMeta(category string, httpStatus *int) error {
	if len(category) > 64 || !protocol.IsErrorCategory(protocol.ErrorCategory(category)) {
		return errors.New("invalid error category")
	}
	if httpStatus != nil && (*httpStatus < 100 || *httpStatus > 599) {
		return errors.New("invalid error http status")
	}
	return nil
}

func isRejection(status protocol.QueueStatus) bool {
	return strings.HasPrefix(string(status), "rejected_")
}

func setCount(counts *protocol.QueueCounts, status protocol.QueueStatus, value int) error {
	switch status {
	case protocol.StatusQueued:
		counts.Queued = value
	case protocol.StatusUploading:
		counts.Uploading = value
	case protocol.StatusUploaded:
		counts.Uploaded = value
	case protocol.StatusUnchanged:
		counts.Unchanged = value
	case protocol.StatusRetryableError:
		counts.RetryableError = value
	case protocol.StatusPermanentConflict:
		counts.PermanentConflict = value
	case protocol.StatusRejectedMissingIdentity:
		counts.RejectedMissingIdentity = value
	case protocol.StatusRejectedAmbiguousMetadata:
		counts.RejectedAmbiguousMetadata = value
	case protocol.StatusRejectedOversized:
		counts.RejectedOversized = value
	case protocol.StatusRejectedQueueFull:
		counts.RejectedQueueFull = value
	case protocol.StatusRejectedInvalidHash:
		counts.RejectedInvalidHash = value
	case protocol.StatusRejectedUnsafeURL:
		counts.RejectedUnsafeURL = value
	case protocol.StatusRejectedUnknownField:
		counts.RejectedUnknownField = value
	case protocol.StatusRejectedInvalidSchema:
		counts.RejectedInvalidSchema = value
	case protocol.StatusRejectedPermission:
		counts.RejectedPermission = value
	default:
		return fmt.Errorf("unknown queue status %q", status)
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored timestamp: %w", err)
	}
	return parsed, nil
}

func timePtr(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func intArg(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func stringArg(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

type Lock struct {
	file *os.File
	path string
}

func AcquireLock(path string) (*Lock, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open queue lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("acquire queue lock: %w", err)
	}
	return &Lock{file: file, path: path}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
