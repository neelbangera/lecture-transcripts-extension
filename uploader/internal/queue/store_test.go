package queue

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

var baseTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func testHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func testJob(number int) protocol.TranscriptJob {
	return protocol.TranscriptJob{
		SchemaVersion:         1,
		Kind:                  "lecture",
		LectureKey:            fmt.Sprintf("eecs491/2026-winter/%03d", number),
		CourseSlug:            "eecs491",
		CourseName:            "EECS 491",
		Term:                  "2026-winter",
		LectureNumber:         number,
		LectureDate:           "2026-02-12",
		SourceURL:             "https://leccap.engin.umich.edu/lecture/123",
		CapturedAt:            "2026-02-12T18:03:22Z",
		Transcript:            "This is a sufficiently long lecture transcript used for queue tests with plenty of words.",
		TimestampedTranscript: "[00:01] This is a sufficiently long lecture transcript used for queue tests.",
		ContentHash:           testHash(fmt.Sprintf("lecture-%d", number)),
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "LectureTranscripts", "queue.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func enqueueJob(t *testing.T, store *Store, job protocol.TranscriptJob) EnqueueResult {
	t.Helper()
	result, err := store.Enqueue(job, baseTime)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return result
}

func claimJob(t *testing.T, store *Store, now time.Time) *Job {
	t.Helper()
	job, err := store.ClaimNext(now)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if job == nil {
		t.Fatal("expected a claimable job")
	}
	return job
}

func intPtr(value int) *int { return &value }

func TestOpenCreatesRestrictedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "LectureTranscripts", "queue.sqlite3")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %#o, want 0700", dirInfo.Mode().Perm())
	}
	dbInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat db: %v", err)
	}
	if dbInfo.Mode().Perm() != 0o600 {
		t.Fatalf("db mode = %#o, want 0600", dbInfo.Mode().Perm())
	}

	enqueueJob(t, store, testJob(1))
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %#o, want 0600", filepath.Base(name), info.Mode().Perm())
		}
	}
}

func TestOpenIsIdempotentAndRejectsFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite3")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	enqueueJob(t, store, testJob(1))
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	counts, err := reopened.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Queued != 1 {
		t.Fatalf("queued = %d, want 1", counts.Queued)
	}
	reopened.Close()

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`UPDATE schema_migrations SET version = 3`); err != nil {
		t.Fatalf("bump schema: %v", err)
	}
	raw.Close()
	if _, err := Open(path); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("future schema error = %v, want ErrUnsupportedSchema", err)
	}
}

func TestSchemaColumnsAndIndexes(t *testing.T) {
	store := newStore(t)
	rows, err := store.db.Query(`PRAGMA table_info(jobs)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns = append(columns, name)
	}
	want := []string{
		"id", "kind", "lecture_key", "content_hash", "job_json", "status", "attempt_count",
		"next_attempt_at", "lease_started_at", "created_at", "updated_at",
		"last_error_category", "last_error_http_status", "remote_content_hash", "remote_file_kind",
	}
	if len(columns) != len(want) {
		t.Fatalf("columns = %v, want %v", columns, want)
	}
	for i := range want {
		if columns[i] != want[i] {
			t.Fatalf("column[%d] = %q, want %q", i, columns[i], want[i])
		}
	}

	indexRows, err := store.db.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'jobs' AND name LIKE 'jobs_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("index query: %v", err)
	}
	defer indexRows.Close()
	var indexes []string
	for indexRows.Next() {
		var name string
		if err := indexRows.Scan(&name); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		indexes = append(indexes, name)
	}
	if len(indexes) != 2 || indexes[0] != "jobs_lecture_idx" || indexes[1] != "jobs_ready_idx" {
		t.Fatalf("indexes = %v", indexes)
	}
}

func TestEnqueuePersistsCanonicalJobBeforeAck(t *testing.T) {
	store := newStore(t)
	job := testJob(6)
	job.SourceURL = "https://LECCAP.ENGIN.UMICH.EDU:443/lecture/123?session=secret#transcript"
	result := enqueueJob(t, store, job)
	if result.Outcome != EnqueueQueued || result.JobID != 1 || result.ExistingStatus != protocol.StatusQueued {
		t.Fatalf("unexpected enqueue result: %+v", result)
	}

	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusQueued || stored.AttemptCount != 0 {
		t.Fatalf("unexpected stored row: %+v", stored)
	}
	if stored.Payload.SourceURL != "https://leccap.engin.umich.edu/lecture/123" {
		t.Fatalf("stored source URL = %q", stored.Payload.SourceURL)
	}
	if stored.Payload.ContentHash != job.ContentHash || stored.LectureKey != job.LectureKey {
		t.Fatalf("stored identity mismatch: %+v", stored)
	}
	if !stored.CreatedAt.Equal(baseTime) || !stored.UpdatedAt.Equal(baseTime) {
		t.Fatalf("timestamps = %s/%s", stored.CreatedAt, stored.UpdatedAt)
	}
	if stored.TargetPath() != "eecs491/006.md" {
		t.Fatalf("target path = %q", stored.TargetPath())
	}
	if stored.TimestampedPath() != "eecs491/timestamped/006.md" {
		t.Fatalf("timestamped path = %q", stored.TimestampedPath())
	}
	jobs, bytes, err := store.Usage()
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if jobs != 1 || bytes <= 0 {
		t.Fatalf("usage = %d jobs/%d bytes", jobs, bytes)
	}
}

func TestEnqueueRejectsInvalidJobWithoutPersisting(t *testing.T) {
	store := newStore(t)
	job := testJob(1)
	job.ContentHash = "not-a-hash"
	if _, err := store.Enqueue(job, baseTime); err == nil {
		t.Fatal("expected invalid job rejection")
	}
	counts, err := store.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Queued != 0 {
		t.Fatalf("queued = %d, want 0", counts.Queued)
	}
}

func TestEnqueueDeduplicatesLectureKeyAndHashPair(t *testing.T) {
	store := newStore(t)
	job := testJob(1)
	first := enqueueJob(t, store, job)
	second := enqueueJob(t, store, job)
	if second.Outcome != EnqueueDuplicate || second.JobID != first.JobID {
		t.Fatalf("duplicate result = %+v, want duplicate of %d", second, first.JobID)
	}
	if second.ExistingStatus != protocol.StatusQueued || second.Action != nil {
		t.Fatalf("duplicate metadata = %+v", second)
	}

	changed := job
	changed.ContentHash = testHash("changed")
	third := enqueueJob(t, store, changed)
	if third.Outcome != EnqueueQueued || third.JobID == first.JobID {
		t.Fatalf("changed hash must create a new row: %+v", third)
	}
	jobs, _, err := store.Usage()
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if jobs != 2 {
		t.Fatalf("jobs = %d, want 2", jobs)
	}
}

func TestEnqueueTerminalDuplicateActions(t *testing.T) {
	store := newStore(t)

	permission := testJob(1)
	enqueueJob(t, store, permission)
	claimed := claimJob(t, store, baseTime)
	if err := store.MarkRejectedPermission(claimed.ID, intPtr(403), baseTime); err != nil {
		t.Fatalf("MarkRejectedPermission: %v", err)
	}
	result := enqueueJob(t, store, permission)
	if result.Outcome != EnqueueDuplicateTerminal || result.ExistingStatus != protocol.StatusRejectedPermission {
		t.Fatalf("permission duplicate = %+v", result)
	}
	if result.Action == nil || *result.Action != protocol.ActionRetryExisting {
		t.Fatalf("permission action = %v", result.Action)
	}

	unsafe := testJob(2)
	enqueueJob(t, store, unsafe)
	claimed = claimJob(t, store, baseTime)
	if err := store.MarkRejected(claimed.ID, protocol.StatusRejectedUnsafeURL, nil, baseTime); err != nil {
		t.Fatalf("MarkRejected: %v", err)
	}
	result = enqueueJob(t, store, unsafe)
	if result.Outcome != EnqueueDuplicateTerminal || result.ExistingStatus != protocol.StatusRejectedUnsafeURL {
		t.Fatalf("unsafe duplicate = %+v", result)
	}
	if result.Action == nil || *result.Action != protocol.ActionDiscardExistingThenRecapture {
		t.Fatalf("unsafe action = %v", result.Action)
	}

	uploaded := testJob(3)
	enqueueJob(t, store, uploaded)
	claimed = claimJob(t, store, baseTime)
	if err := store.MarkUploaded(claimed.ID, baseTime); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	result = enqueueJob(t, store, uploaded)
	if result.Outcome != EnqueueDuplicate || result.ExistingStatus != protocol.StatusUploaded || result.Action != nil {
		t.Fatalf("uploaded duplicate = %+v", result)
	}
}

func TestEnqueueQueueFullByCount(t *testing.T) {
	store := newStore(t)
	ts := formatTime(baseTime)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 1; i <= config.QueueMaxJobs; i++ {
		if _, err := tx.Exec(
			`INSERT INTO jobs (lecture_key, content_hash, job_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("eecs491/2026-winter/%03d", i), testHash(fmt.Sprintf("filler-%d", i)), "{}", protocol.StatusUploaded, ts, ts,
		); err != nil {
			t.Fatalf("insert filler %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	result := enqueueJob(t, store, testJob(1))
	if result.Outcome != EnqueueRejectedQueueFull || result.JobID != 0 {
		t.Fatalf("queue full result = %+v", result)
	}
	jobs, _, err := store.Usage()
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if jobs != config.QueueMaxJobs {
		t.Fatalf("jobs = %d, want %d", jobs, config.QueueMaxJobs)
	}
}

func TestWouldExceedLimits(t *testing.T) {
	if wouldExceedLimits(config.QueueMaxJobs-1, 0, 1) {
		t.Fatal("under job limit must be accepted")
	}
	if !wouldExceedLimits(config.QueueMaxJobs, 0, 1) {
		t.Fatal("job limit must be enforced")
	}
	if !wouldExceedLimits(0, config.QueueMaxBytes, 1) {
		t.Fatal("byte limit must be enforced")
	}
	if wouldExceedLimits(0, config.QueueMaxBytes-1, 1) {
		t.Fatal("under byte limit must be accepted")
	}
}

func TestClaimSerialAcrossStores(t *testing.T) {
	store := newStore(t)
	enqueueJob(t, store, testJob(1))
	enqueueJob(t, store, testJob(2))

	second, err := Open(store.Path())
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed []int64
	)
	for _, candidate := range []*Store{store, second} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			job, err := s.ClaimNext(baseTime)
			if err != nil {
				t.Errorf("ClaimNext: %v", err)
				return
			}
			if job == nil {
				return
			}
			mu.Lock()
			claimed = append(claimed, job.ID)
			mu.Unlock()
		}(candidate)
	}
	wg.Wait()

	if len(claimed) != 2 || claimed[0] == claimed[1] {
		t.Fatalf("claimed = %v, want two distinct jobs", claimed)
	}
	remaining, err := store.ClaimNext(baseTime)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if remaining != nil {
		t.Fatalf("unexpected third claim: %+v", remaining)
	}
}

func TestClaimSkipsFutureRetriesUntilDue(t *testing.T) {
	store := newStore(t)
	enqueueJob(t, store, testJob(1))
	claimed := claimJob(t, store, baseTime)
	dueAt := baseTime.Add(30 * time.Second)
	if err := store.MarkRetryableError(claimed.ID, "internal", intPtr(503), dueAt, baseTime); err != nil {
		t.Fatalf("MarkRetryableError: %v", err)
	}

	early, err := store.ClaimNext(baseTime.Add(10 * time.Second))
	if err != nil {
		t.Fatalf("ClaimNext early: %v", err)
	}
	if early != nil {
		t.Fatalf("future retry claimed early: %+v", early)
	}
	promoted, err := store.PromoteDueRetries(baseTime.Add(29 * time.Second))
	if err != nil || promoted != 0 {
		t.Fatalf("PromoteDueRetries early = %d, %v", promoted, err)
	}
	promoted, err = store.PromoteDueRetries(dueAt)
	if err != nil || promoted != 1 {
		t.Fatalf("PromoteDueRetries due = %d, %v", promoted, err)
	}
	retried := claimJob(t, store, dueAt)
	if retried.AttemptCount != 1 || retried.NextAttemptAt != nil {
		t.Fatalf("retried job = %+v", retried)
	}
}

func TestRecoverStaleLeases(t *testing.T) {
	store := newStore(t)
	enqueueJob(t, store, testJob(1))
	enqueueJob(t, store, testJob(2))
	first := claimJob(t, store, baseTime)
	second := claimJob(t, store, baseTime)

	recovered, err := store.RecoverStaleLeases(baseTime.Add(5*time.Minute), config.UploadLease)
	if err != nil || recovered != 0 {
		t.Fatalf("fresh lease recovery = %d, %v", recovered, err)
	}
	recovered, err = store.RecoverStaleLeases(baseTime.Add(11*time.Minute), config.UploadLease)
	if err != nil || recovered != 2 {
		t.Fatalf("stale lease recovery = %d, %v", recovered, err)
	}
	for _, id := range []int64{first.ID, second.ID} {
		job, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if job.Status != protocol.StatusQueued || job.AttemptCount != 1 || job.LeaseStartedAt != nil {
			t.Fatalf("recovered job = %+v", job)
		}
	}

	if _, err := store.db.Exec(`UPDATE jobs SET lease_started_at = NULL WHERE id = ?`, first.ID); err != nil {
		t.Fatalf("clear lease: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE jobs SET status = ? WHERE id = ?`, protocol.StatusUploading, first.ID); err != nil {
		t.Fatalf("restore uploading: %v", err)
	}
	recovered, err = store.RecoverStaleLeases(baseTime.Add(12*time.Minute), config.UploadLease)
	if err != nil || recovered != 1 {
		t.Fatalf("null lease recovery = %d, %v", recovered, err)
	}
}

func TestStatusTransitions(t *testing.T) {
	store := newStore(t)

	uploaded := testJob(1)
	enqueueJob(t, store, uploaded)
	claimed := claimJob(t, store, baseTime)
	if err := store.MarkUploaded(claimed.ID, baseTime); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	job, err := store.Get(claimed.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.Status != protocol.StatusUploaded || job.AttemptCount != 0 || job.LastErrorCategory != nil {
		t.Fatalf("uploaded job = %+v", job)
	}

	unchanged := testJob(2)
	enqueueJob(t, store, unchanged)
	claimed = claimJob(t, store, baseTime)
	if err := store.MarkUnchanged(claimed.ID, unchanged.ContentHash, baseTime); err != nil {
		t.Fatalf("MarkUnchanged: %v", err)
	}
	job, err = store.Get(claimed.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.Status != protocol.StatusUnchanged || job.RemoteContentHash == nil || *job.RemoteContentHash != unchanged.ContentHash {
		t.Fatalf("unchanged job = %+v", job)
	}
	if job.RemoteFileKind == nil || *job.RemoteFileKind != "file" {
		t.Fatalf("unchanged kind = %v", job.RemoteFileKind)
	}

	retryable := testJob(3)
	enqueueJob(t, store, retryable)
	claimed = claimJob(t, store, baseTime)
	dueAt := baseTime.Add(2 * time.Minute)
	if err := store.MarkRetryableError(claimed.ID, "internal", intPtr(429), dueAt, baseTime); err != nil {
		t.Fatalf("MarkRetryableError: %v", err)
	}
	job, err = store.Get(claimed.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.Status != protocol.StatusRetryableError || job.AttemptCount != 1 || job.NextAttemptAt == nil || !job.NextAttemptAt.Equal(dueAt) {
		t.Fatalf("retryable job = %+v", job)
	}
	if job.LastErrorCategory == nil || *job.LastErrorCategory != "internal" || job.LastErrorHTTPStatus == nil || *job.LastErrorHTTPStatus != 429 {
		t.Fatalf("retryable error metadata = %+v", job)
	}

	conflict := testJob(4)
	enqueueJob(t, store, conflict)
	claimed = claimJob(t, store, baseTime)
	remoteHash := testHash("remote")
	if err := store.MarkPermanentConflict(claimed.ID, "internal", intPtr(409), &remoteHash, "file", baseTime); err != nil {
		t.Fatalf("MarkPermanentConflict: %v", err)
	}
	job, err = store.Get(claimed.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.Status != protocol.StatusPermanentConflict || job.AttemptCount != 1 {
		t.Fatalf("conflict job = %+v", job)
	}
	if job.RemoteContentHash == nil || *job.RemoteContentHash != remoteHash || job.RemoteFileKind == nil || *job.RemoteFileKind != "file" {
		t.Fatalf("conflict remote metadata = %+v", job)
	}

	rejected := testJob(5)
	enqueueJob(t, store, rejected)
	claimed = claimJob(t, store, baseTime)
	if err := store.MarkRejectedPermission(claimed.ID, intPtr(404), baseTime); err != nil {
		t.Fatalf("MarkRejectedPermission: %v", err)
	}
	job, err = store.Get(claimed.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.Status != protocol.StatusRejectedPermission || job.AttemptCount != 1 || job.LastErrorCategory == nil || *job.LastErrorCategory != "rejected_permission" {
		t.Fatalf("rejected job = %+v", job)
	}
}

func TestInvalidTransitionsAreRejected(t *testing.T) {
	store := newStore(t)
	enqueueJob(t, store, testJob(1))
	if err := store.MarkUploaded(1, baseTime); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("MarkUploaded on queued = %v", err)
	}
	claimed := claimJob(t, store, baseTime)
	if err := store.MarkUnchanged(claimed.ID, "bad-hash", baseTime); err == nil {
		t.Fatal("MarkUnchanged must reject invalid remote hash")
	}
	if err := store.MarkRetryableError(claimed.ID, "not-a-category", nil, baseTime, baseTime); err == nil {
		t.Fatal("MarkRetryableError must reject unknown category")
	}
	if err := store.MarkRetryableError(claimed.ID, "internal", intPtr(99), baseTime, baseTime); err == nil {
		t.Fatal("MarkRetryableError must reject invalid http status")
	}
	if err := store.MarkRetryableError(claimed.ID, "internal", nil, time.Time{}, baseTime); err == nil {
		t.Fatal("MarkRetryableError must require a next attempt time")
	}
	if err := store.MarkPermanentConflict(claimed.ID, "internal", nil, nil, "tarball", baseTime); err == nil {
		t.Fatal("MarkPermanentConflict must reject unknown file kind")
	}
	if err := store.MarkRejected(claimed.ID, protocol.StatusQueued, nil, baseTime); err == nil {
		t.Fatal("MarkRejected must reject non-rejection status")
	}
}

func TestRetryEligibility(t *testing.T) {
	store := newStore(t)

	enqueueJob(t, store, testJob(1))
	claimed := claimJob(t, store, baseTime)
	if err := store.MarkRetryableError(claimed.ID, "internal", nil, baseTime.Add(time.Minute), baseTime); err != nil {
		t.Fatalf("MarkRetryableError: %v", err)
	}
	status, err := store.RetryJob(claimed.ID, baseTime)
	if err != nil || status != protocol.StatusQueued {
		t.Fatalf("RetryJob retryable = %s, %v", status, err)
	}
	job, err := store.Get(claimed.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.NextAttemptAt != nil || job.LeaseStartedAt != nil || job.Status != protocol.StatusQueued {
		t.Fatalf("retried job = %+v", job)
	}

	if _, err := store.RetryJob(9999, baseTime); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("missing job error = %v", err)
	}
	status, err = store.RetryJob(claimed.ID, baseTime)
	if !errors.Is(err, ErrNotEligible) || status != protocol.StatusQueued {
		t.Fatalf("ineligible retry = %s, %v", status, err)
	}

	conflict := testJob(2)
	enqueueJob(t, store, conflict)
	claimed = claimJob(t, store, baseTime)
	if err := store.MarkPermanentConflict(claimed.ID, "internal", nil, nil, "missing", baseTime); err != nil {
		t.Fatalf("MarkPermanentConflict: %v", err)
	}
	if status, err = store.RetryJob(claimed.ID, baseTime); err != nil || status != protocol.StatusQueued {
		t.Fatalf("RetryJob conflict = %s, %v", status, err)
	}

	rejected := testJob(3)
	enqueueJob(t, store, rejected)
	claimed = claimJob(t, store, baseTime)
	if err := store.MarkRejectedPermission(claimed.ID, nil, baseTime); err != nil {
		t.Fatalf("MarkRejectedPermission: %v", err)
	}
	if status, err = store.RetryJob(claimed.ID, baseTime); err != nil || status != protocol.StatusQueued {
		t.Fatalf("RetryJob rejected_permission = %s, %v", status, err)
	}
}

func TestDiscardRules(t *testing.T) {
	store := newStore(t)

	rejected := testJob(1)
	enqueueJob(t, store, rejected)
	claimed := claimJob(t, store, baseTime)
	if err := store.MarkRejected(claimed.ID, protocol.StatusRejectedOversized, nil, baseTime); err != nil {
		t.Fatalf("MarkRejected: %v", err)
	}
	if status, err := store.DiscardJob(claimed.ID, baseTime); err != nil || status != protocol.StatusRejectedOversized {
		t.Fatalf("DiscardJob rejected = %s, %v", status, err)
	}
	if _, err := store.Get(claimed.ID); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("discarded job still present: %v", err)
	}

	conflict := testJob(2)
	enqueueJob(t, store, conflict)
	claimed = claimJob(t, store, baseTime)
	if err := store.MarkPermanentConflict(claimed.ID, "internal", nil, nil, "file", baseTime); err != nil {
		t.Fatalf("MarkPermanentConflict: %v", err)
	}
	if _, err := store.DiscardJob(claimed.ID, baseTime); err != nil {
		t.Fatalf("DiscardJob conflict: %v", err)
	}

	queued := testJob(3)
	enqueueJob(t, store, queued)
	if status, err := store.DiscardJob(3, baseTime); !errors.Is(err, ErrNotEligible) || status != protocol.StatusQueued {
		t.Fatalf("DiscardJob queued = %s, %v", status, err)
	}
	claimed = claimJob(t, store, baseTime)
	if status, err := store.DiscardJob(claimed.ID, baseTime); !errors.Is(err, ErrNotEligible) || status != protocol.StatusUploading {
		t.Fatalf("DiscardJob uploading = %s, %v", status, err)
	}
	if err := store.MarkUploaded(claimed.ID, baseTime); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	if status, err := store.DiscardJob(claimed.ID, baseTime); !errors.Is(err, ErrNotEligible) || status != protocol.StatusUploaded {
		t.Fatalf("DiscardJob uploaded = %s, %v", status, err)
	}
	if _, err := store.DiscardJob(9999, baseTime); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("DiscardJob missing = %v", err)
	}
}

func TestStatusPagePagination(t *testing.T) {
	store := newStore(t)
	for i := 1; i <= 5; i++ {
		enqueueJob(t, store, testJob(i))
	}

	page, next, err := store.StatusPage(nil, 2)
	if err != nil {
		t.Fatalf("StatusPage: %v", err)
	}
	if len(page) != 2 || page[0].JobID != 5 || page[1].JobID != 4 {
		t.Fatalf("page 1 = %+v", page)
	}
	if next == nil || *next != 4 {
		t.Fatalf("page 1 cursor = %v", next)
	}
	if page[0].TargetPath != "eecs491/005.md" || page[0].Status != protocol.StatusQueued {
		t.Fatalf("page 1 summary = %+v", page[0])
	}
	if page[0].UpdatedAt == nil || page[0].NextAttemptAt != nil {
		t.Fatalf("page 1 timestamps = %+v", page[0])
	}

	page, next, err = store.StatusPage(next, 2)
	if err != nil {
		t.Fatalf("StatusPage: %v", err)
	}
	if len(page) != 2 || page[0].JobID != 3 || page[1].JobID != 2 {
		t.Fatalf("page 2 = %+v", page)
	}
	if next == nil || *next != 2 {
		t.Fatalf("page 2 cursor = %v", next)
	}

	page, next, err = store.StatusPage(next, 2)
	if err != nil {
		t.Fatalf("StatusPage: %v", err)
	}
	if len(page) != 1 || page[0].JobID != 1 || next != nil {
		t.Fatalf("page 3 = %+v cursor=%v", page, next)
	}

	page, next, err = store.StatusPage(nil, 50)
	if err != nil {
		t.Fatalf("StatusPage full: %v", err)
	}
	if len(page) != 5 || next != nil {
		t.Fatalf("full page = %d summaries, cursor=%v", len(page), next)
	}
}

func TestCountsTrackEveryStatus(t *testing.T) {
	store := newStore(t)
	enqueueJob(t, store, testJob(1))
	enqueueJob(t, store, testJob(2))
	claimed := claimJob(t, store, baseTime)
	if err := store.MarkUploaded(claimed.ID, baseTime); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	claimed = claimJob(t, store, baseTime)
	if err := store.MarkRetryableError(claimed.ID, "internal", nil, baseTime.Add(time.Minute), baseTime); err != nil {
		t.Fatalf("MarkRetryableError: %v", err)
	}

	counts, err := store.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Uploaded != 1 || counts.RetryableError != 1 {
		t.Fatalf("counts = %+v", counts)
	}
	if counts.Queued != 0 || counts.PermanentConflict != 0 || counts.RejectedPermission != 0 {
		t.Fatalf("unexpected counts = %+v", counts)
	}
}

func TestPruneDeletesOnlyOldTerminalRowsUnderPressure(t *testing.T) {
	store := newStore(t)
	old := formatTime(baseTime.Add(-8 * 24 * time.Hour))
	recent := formatTime(baseTime.Add(-time.Hour))
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 1; i <= config.QueueMaxJobs; i++ {
		if _, err := tx.Exec(
			`INSERT INTO jobs (lecture_key, content_hash, job_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("eecs491/2026-winter/%03d", i), testHash(fmt.Sprintf("old-uploaded-%d", i)), "{}", protocol.StatusUploaded, old, old,
		); err != nil {
			t.Fatalf("insert old uploaded: %v", err)
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO jobs (lecture_key, content_hash, job_json, status, created_at, updated_at, last_error_category) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"eecs491/2026-winter/900", testHash("old-retryable"), "{}", protocol.StatusRetryableError, old, old, "internal",
	); err != nil {
		t.Fatalf("insert retryable: %v", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO jobs (lecture_key, content_hash, job_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"eecs491/2026-winter/901", testHash("recent-uploaded"), "{}", protocol.StatusUploaded, recent, recent,
	); err != nil {
		t.Fatalf("insert recent uploaded: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	deleted, err := store.Prune(baseTime)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	counts, err := store.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Uploaded != 498 {
		t.Fatalf("uploaded = %d, want 498", counts.Uploaded)
	}
	if counts.RetryableError != 1 {
		t.Fatalf("retryable_error must never be pruned: %+v", counts)
	}
	if counts.Queued != 0 {
		t.Fatalf("unexpected queued rows: %+v", counts)
	}
}

func TestPruneKeepsRecentRowsAndProtectedStatuses(t *testing.T) {
	store := newStore(t)
	old := formatTime(baseTime.Add(-8 * 24 * time.Hour))
	recent := formatTime(baseTime.Add(-time.Hour))
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 1; i <= config.QueueMaxJobs; i++ {
		if _, err := tx.Exec(
			`INSERT INTO jobs (lecture_key, content_hash, job_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("eecs491/2026-winter/%03d", i), testHash(fmt.Sprintf("old-rejected-%d", i)), "{}", protocol.StatusRejectedPermission, old, old,
		); err != nil {
			t.Fatalf("insert rejected: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(
			`INSERT INTO jobs (lecture_key, content_hash, job_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("eecs491/2026-winter/%03d", 910+i), testHash(fmt.Sprintf("recent-uploaded-%d", i)), "{}", protocol.StatusUploaded, recent, recent,
		); err != nil {
			t.Fatalf("insert recent: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	deleted, err := store.Prune(baseTime)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
	counts, err := store.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.RejectedPermission != config.QueueMaxJobs || counts.Uploaded != 2 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestAcquireLockIsExclusiveAndReusable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "queue.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %#o, want 0600", info.Mode().Perm())
	}
	if _, err := AcquireLock(path); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second lock error = %v, want ErrAlreadyRunning", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("leftover lock file must be harmless: %v", err)
	}
	var empty *Lock
	if err := empty.Release(); err != nil {
		t.Fatalf("nil Release: %v", err)
	}
	if _, err := AcquireLock(""); err == nil {
		t.Fatal("empty lock path must fail")
	}
}

func TestPlainOnlyTargetPath(t *testing.T) {
	job := testJob(1)
	job.TimestampedTranscript = ""
	stored := Job{Payload: job}
	if got, want := stored.TargetPath(), "eecs491/001.md"; got != want {
		t.Fatalf("plain-only target path = %q, want %q", got, want)
	}
	withTimestamps := Job{Payload: testJob(2)}
	if got, want := withTimestamps.TargetPath(), "eecs491/002.md"; got != want {
		t.Fatalf("timestamped target path = %q, want %q", got, want)
	}
}

func TestMigrationFromVersion1AddsKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.sqlite3")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	legacySchema := `
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  lecture_key TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  job_json TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT,
  lease_started_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_error_category TEXT,
  last_error_http_status INTEGER,
  remote_content_hash TEXT,
  remote_file_kind TEXT,
  UNIQUE (lecture_key, content_hash),
  CHECK (attempt_count >= 0),
  CHECK (length(content_hash) = 64)
);
CREATE INDEX jobs_ready_idx ON jobs (status, next_attempt_at, created_at);
CREATE INDEX jobs_lecture_idx ON jobs (lecture_key, created_at);
INSERT INTO schema_migrations(version) VALUES (1);
`
	if _, err := raw.Exec(legacySchema); err != nil {
		t.Fatalf("apply legacy schema: %v", err)
	}
	job := testJob(1)
	payload, err := job.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	if _, err := raw.Exec(
		`INSERT INTO jobs (lecture_key, content_hash, job_json, status, attempt_count, created_at, updated_at) VALUES (?, ?, ?, 'uploaded', 0, ?, ?)`,
		job.LectureKey, job.ContentHash, string(payload), "2026-02-12T18:03:22Z", "2026-02-12T18:03:22Z",
	); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	raw.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open migrated store: %v", err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}
	var kind string
	if err := store.db.QueryRow(`SELECT kind FROM jobs WHERE id = 1`).Scan(&kind); err != nil {
		t.Fatalf("read kind: %v", err)
	}
	if kind != protocol.KindLecture {
		t.Fatalf("migrated kind = %q, want lecture", kind)
	}
	job2 := testJob(2)
	job2.Kind = protocol.KindDiscussion
	if _, err := store.Enqueue(job2, baseTime); err != nil {
		t.Fatalf("enqueue discussion after migration: %v", err)
	}
}

func TestDiscussionPathsAndFirstCaptureWins(t *testing.T) {
	if got, want := TargetPath(protocol.KindDiscussion, "eecs491", 1), "eecs491/discussions/001.md"; got != want {
		t.Fatalf("discussion target path = %q, want %q", got, want)
	}
	if got, want := TimestampedPath(protocol.KindDiscussion, "eecs491", 1), "eecs491/discussions/timestamped/001.md"; got != want {
		t.Fatalf("discussion timestamped path = %q, want %q", got, want)
	}

	store := newStore(t)
	first := testJob(1)
	first.Kind = protocol.KindDiscussion
	first.TimestampedTranscript = ""
	result, err := store.Enqueue(first, baseTime)
	if err != nil {
		t.Fatalf("Enqueue first discussion: %v", err)
	}
	if result.Outcome != EnqueueQueued {
		t.Fatalf("first discussion outcome = %v, want queued", result.Outcome)
	}

	secondSection := testJob(1)
	secondSection.Kind = protocol.KindDiscussion
	secondSection.ContentHash = testHash("other-section")
	result, err = store.Enqueue(secondSection, baseTime)
	if err != nil {
		t.Fatalf("Enqueue second section: %v", err)
	}
	if result.Outcome != EnqueueDuplicate {
		t.Fatalf("second section outcome = %v, want duplicate", result.Outcome)
	}

	counts, err := store.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Queued != 1 {
		t.Fatalf("queued = %d, want 1", counts.Queued)
	}

	lecture := testJob(1)
	result, err = store.Enqueue(lecture, baseTime)
	if err != nil {
		t.Fatalf("Enqueue lecture 1: %v", err)
	}
	if result.Outcome != EnqueueQueued {
		t.Fatalf("lecture 1 outcome = %v, want queued (kind disambiguates)", result.Outcome)
	}
}
