package processor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/auth"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/github"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/host"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/queue"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/retry"
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
		Transcript:            "This is a sufficiently long lecture transcript used for processor tests with plenty of words.",
		TimestampedTranscript: "[00:01] This is a sufficiently long lecture transcript used for processor tests.",
		ContentHash:           testHash(fmt.Sprintf("lecture-%d", number)),
	}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeAuth struct {
	mu             sync.Mutex
	state          protocol.AuthState
	challenge      *auth.Challenge
	beginChallenge *auth.Challenge
	pollStates     []protocol.AuthState
	beginCalls     int
	pollCalls      int
	resetCalls     int
	resetErr       error
}

func newFakeAuth(state protocol.AuthState) *fakeAuth {
	return &fakeAuth{state: state}
}

func (f *fakeAuth) State() protocol.AuthState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeAuth) Challenge() *auth.Challenge {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.challenge == nil {
		return nil
	}
	copied := *f.challenge
	return &copied
}

func (f *fakeAuth) Begin(context.Context) (*auth.Challenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beginCalls++
	if f.beginChallenge == nil {
		return nil, nil
	}
	copied := *f.beginChallenge
	f.challenge = &copied
	f.state = protocol.AuthAuthorizing
	return &copied, nil
}

func (f *fakeAuth) Poll(context.Context) (protocol.AuthState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollCalls++
	if len(f.pollStates) > 0 {
		f.state = f.pollStates[0]
		f.pollStates = f.pollStates[1:]
		if f.state != protocol.AuthAuthorizing {
			f.challenge = nil
		}
	}
	return f.state, nil
}

func (f *fakeAuth) Reset() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resetCalls++
	f.state = protocol.AuthNotConnected
	f.challenge = nil
	return f.resetErr
}

type fakeRemote struct {
	kind protocol.RemoteFileKind
	hash *string
}

type fakePublisher struct {
	mu           sync.Mutex
	remote       map[string]fakeRemote
	contents     map[string][]byte
	publishErr   error
	inspectErr   error
	publishCalls int
	createCalls  int
	inspectCalls int
	onPublish    func()
}

func newFakePublisher() *fakePublisher {
	return &fakePublisher{remote: map[string]fakeRemote{}, contents: map[string][]byte{}}
}

func (f *fakePublisher) InspectFile(_ context.Context, path string) (github.RemoteFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls++
	if f.inspectErr != nil {
		return github.RemoteFile{}, f.inspectErr
	}
	remote, ok := f.remote[path]
	if !ok {
		return github.RemoteFile{Path: path, Kind: protocol.RemoteMissing}, nil
	}
	return github.RemoteFile{Path: path, Kind: remote.kind, ContentHash: remote.hash}, nil
}

func (f *fakePublisher) Publish(_ context.Context, path string, job protocol.TranscriptJob, content []byte) (github.PublishResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishCalls++
	f.contents[path] = content
	if f.onPublish != nil {
		f.onPublish()
	}
	if f.publishErr != nil {
		return github.PublishResult{}, f.publishErr
	}
	remote, ok := f.remote[path]
	if !ok {
		hash := job.ContentHash
		f.remote[path] = fakeRemote{kind: protocol.RemoteFile, hash: &hash}
		f.createCalls++
		status := 201
		return github.PublishResult{
			Outcome:    github.OutcomeCreated,
			Remote:     github.RemoteFile{Path: path, Kind: protocol.RemoteMissing},
			HTTPStatus: &status,
		}, nil
	}
	if remote.kind == protocol.RemoteFile && remote.hash != nil && *remote.hash == job.ContentHash {
		status := 200
		return github.PublishResult{
			Outcome:    github.OutcomeUnchanged,
			Remote:     github.RemoteFile{Path: path, Kind: protocol.RemoteFile, ContentHash: remote.hash},
			HTTPStatus: &status,
		}, nil
	}
	return github.PublishResult{
		Outcome: github.OutcomeConflict,
		Remote:  github.RemoteFile{Path: path, Kind: remote.kind, ContentHash: remote.hash},
	}, nil
}

func newTestProcessor(t *testing.T) (*Processor, *queue.Store, *fakeAuth, *fakePublisher, *testClock) {
	t.Helper()
	store, err := queue.Open(filepath.Join(t.TempDir(), "LectureTranscripts", "queue.sqlite3"))
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	clock := &testClock{now: baseTime}
	authn := newFakeAuth(protocol.AuthConnected)
	publisher := newFakePublisher()
	processor, err := New(Config{
		Store:     store,
		Auth:      authn,
		Publisher: publisher,
		Backoff:   retry.NewWithSource(func() float64 { return 0.5 }),
		Now:       clock.Now,
	})
	if err != nil {
		t.Fatalf("processor.New: %v", err)
	}
	return processor, store, authn, publisher, clock
}

func submitRequest(requestID string, job protocol.TranscriptJob) protocol.Request {
	return protocol.Request{
		Type:            protocol.RequestSubmitJob,
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       requestID,
		Job:             &job,
	}
}

func statusRequest(requestID string, before *int64, limit *int) protocol.Request {
	return protocol.Request{
		Type:            protocol.RequestStatus,
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       requestID,
		BeforeJobID:     before,
		Limit:           limit,
	}
}

func intPtr(value int) *int { return &value }

func assertValidResponse(t *testing.T, response any) {
	t.Helper()
	if _, err := protocol.EncodeResponse(response); err != nil {
		t.Fatalf("response failed protocol validation: %v (%#v)", err, response)
	}
}

func submitJob(t *testing.T, p *Processor, requestID string, job protocol.TranscriptJob) protocol.Ack {
	t.Helper()
	response, err := p.HandleRequest(context.Background(), submitRequest(requestID, job))
	if err != nil {
		t.Fatalf("HandleRequest(submit): %v", err)
	}
	ack, ok := response.(protocol.Ack)
	if !ok {
		t.Fatalf("submit response type = %T, want protocol.Ack", response)
	}
	assertValidResponse(t, ack)
	return ack
}

func TestSubmitEnqueueProcessUploaded(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	ctx := context.Background()
	job := testJob(6)

	ack := submitJob(t, p, "req-submit-1", job)
	if ack.Status != protocol.AckQueued || ack.JobID == nil || *ack.JobID != 1 {
		t.Fatalf("ack = %+v, want queued job 1", ack)
	}
	if ack.LectureKey == nil || *ack.LectureKey != job.LectureKey || ack.ContentHash == nil || *ack.ContentHash != job.ContentHash {
		t.Fatalf("ack identity = %+v", ack)
	}
	if ack.ExistingStatus != nil || ack.Action != nil {
		t.Fatalf("new submit ack carried duplicate metadata: %+v", ack)
	}
	counts, err := store.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Queued != 1 {
		t.Fatalf("queued = %d, want 1", counts.Queued)
	}

	state, err := p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if state != protocol.DrainWorking {
		t.Fatalf("drain state = %s, want working", state)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusUploaded {
		t.Fatalf("job status = %s, want uploaded", stored.Status)
	}
	if publisher.createCalls != 2 {
		t.Fatalf("create calls = %d, want 2", publisher.createCalls)
	}
	content, ok := publisher.contents[stored.TargetPath()]
	if !ok {
		t.Fatalf("publisher never received content for %s", stored.TargetPath())
	}
	if !bytes.Contains(content, []byte(job.ContentHash)) {
		t.Fatal("rendered markdown must carry the job content hash")
	}

	state, err = p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce idle: %v", err)
	}
	if state != protocol.DrainIdle || p.DrainState() != protocol.DrainIdle {
		t.Fatalf("drain state = %s/%s, want idle", state, p.DrainState())
	}
}

func TestSubmitDuplicateReturnsAlreadyQueued(t *testing.T) {
	p, store, _, _, _ := newTestProcessor(t)
	job := testJob(1)
	first := submitJob(t, p, "req-1", job)
	second := submitJob(t, p, "req-2", job)
	if second.Status != protocol.AckAlreadyQueued || second.JobID == nil || *second.JobID != *first.JobID {
		t.Fatalf("duplicate ack = %+v", second)
	}
	if second.ExistingStatus == nil || *second.ExistingStatus != protocol.StatusQueued {
		t.Fatalf("duplicate existing status = %v", second.ExistingStatus)
	}
	jobs, _, err := store.Usage()
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if jobs != 1 {
		t.Fatalf("jobs = %d, want 1", jobs)
	}
}

func TestSameHashMarksUnchanged(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	job := testJob(1)
	publisher.remote[queue.TargetPath(job.Kind, job.CourseSlug, job.LectureNumber)] = fakeRemote{kind: protocol.RemoteFile, hash: &job.ContentHash}
	publisher.remote[queue.TimestampedPath(job.Kind, job.CourseSlug, job.LectureNumber)] = fakeRemote{kind: protocol.RemoteFile, hash: &job.ContentHash}

	submitJob(t, p, "req-1", job)
	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusUnchanged {
		t.Fatalf("status = %s, want unchanged", stored.Status)
	}
	if stored.RemoteContentHash == nil || *stored.RemoteContentHash != job.ContentHash {
		t.Fatalf("remote hash = %v", stored.RemoteContentHash)
	}
	if publisher.createCalls != 0 {
		t.Fatalf("unchanged must not create a file")
	}
}

func TestDifferentHashMarksPermanentConflict(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	job := testJob(1)
	remoteHash := testHash("remote-different")
	publisher.remote[queue.TargetPath(job.Kind, job.CourseSlug, job.LectureNumber)] = fakeRemote{kind: protocol.RemoteFile, hash: &remoteHash}

	submitJob(t, p, "req-1", job)
	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusPermanentConflict {
		t.Fatalf("status = %s, want permanent_conflict", stored.Status)
	}
	if stored.RemoteContentHash == nil || *stored.RemoteContentHash != remoteHash {
		t.Fatalf("remote hash = %v, want %s", stored.RemoteContentHash, remoteHash)
	}
	if stored.RemoteFileKind == nil || *stored.RemoteFileKind != string(protocol.RemoteFile) {
		t.Fatalf("remote kind = %v", stored.RemoteFileKind)
	}
	if publisher.createCalls != 0 {
		t.Fatal("conflict must never overwrite the remote file")
	}

	duplicate := submitJob(t, p, "req-2", job)
	if duplicate.Status != protocol.AckAlreadyQueued || duplicate.ExistingStatus == nil || *duplicate.ExistingStatus != protocol.StatusPermanentConflict {
		t.Fatalf("conflict duplicate ack = %+v", duplicate)
	}
}

func TestMalformedRemoteMarksPermanentConflict(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	job := testJob(1)
	publisher.remote[queue.TargetPath(job.Kind, job.CourseSlug, job.LectureNumber)] = fakeRemote{kind: protocol.RemoteMalformed}

	submitJob(t, p, "req-1", job)
	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusPermanentConflict {
		t.Fatalf("status = %s, want permanent_conflict", stored.Status)
	}
	if stored.RemoteFileKind == nil || *stored.RemoteFileKind != string(protocol.RemoteMalformed) {
		t.Fatalf("remote kind = %v, want malformed", stored.RemoteFileKind)
	}
	if stored.RemoteContentHash != nil {
		t.Fatalf("malformed remote must not expose a trusted hash: %v", stored.RemoteContentHash)
	}
}

func TestRetryableErrorSchedulesThenPromotes(t *testing.T) {
	p, store, _, publisher, clock := newTestProcessor(t)
	ctx := context.Background()
	job := testJob(1)
	publisher.publishErr = &github.Error{Category: github.CategoryRetryable, HTTPStatus: 503, Op: "contents.get"}

	submitJob(t, p, "req-1", job)
	if _, err := p.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusRetryableError || stored.AttemptCount != 1 {
		t.Fatalf("retryable job = %+v", stored)
	}
	if stored.NextAttemptAt == nil || !stored.NextAttemptAt.Equal(baseTime.Add(5*time.Second)) {
		t.Fatalf("next attempt = %v, want %v", stored.NextAttemptAt, baseTime.Add(5*time.Second))
	}
	if stored.LastErrorCategory == nil || *stored.LastErrorCategory != string(protocol.ErrorInternal) {
		t.Fatalf("error category = %v", stored.LastErrorCategory)
	}
	if stored.LastErrorHTTPStatus == nil || *stored.LastErrorHTTPStatus != 503 {
		t.Fatalf("http status = %v", stored.LastErrorHTTPStatus)
	}

	state, err := p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce early: %v", err)
	}
	if state != protocol.DrainWaitingForBackoff || p.DrainState() != protocol.DrainWaitingForBackoff {
		t.Fatalf("drain state = %s/%s, want waiting_for_backoff", state, p.DrainState())
	}
	if publisher.publishCalls != 1 {
		t.Fatalf("future retry must not be claimed: publish calls = %d", publisher.publishCalls)
	}

	publisher.publishErr = nil
	clock.Advance(5 * time.Second)
	if p.DrainState() != protocol.DrainWorking {
		t.Fatalf("drain state with a due retry = %s, want working", p.DrainState())
	}
	state, err = p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce due: %v", err)
	}
	if state != protocol.DrainWorking {
		t.Fatalf("drain state = %s, want working", state)
	}
	stored, err = store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusUploaded || stored.AttemptCount != 1 {
		t.Fatalf("promoted job = %+v", stored)
	}
	if publisher.publishCalls != 3 {
		t.Fatalf("publish calls = %d, want 3", publisher.publishCalls)
	}
}

func TestStaleLeaseRecovery(t *testing.T) {
	p, store, _, _, clock := newTestProcessor(t)
	ctx := context.Background()
	job := testJob(1)
	submitJob(t, p, "req-1", job)

	claimed, err := store.ClaimNext(baseTime)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext: %v, %v", claimed, err)
	}
	if p.DrainState() != protocol.DrainIdle {
		t.Fatalf("orphaned uploading row must not report working: %s", p.DrainState())
	}
	clock.Advance(11 * time.Minute)

	state, err := p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if state != protocol.DrainWorking {
		t.Fatalf("drain state = %s, want working", state)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusUploaded || stored.AttemptCount != 1 {
		t.Fatalf("recovered job = %+v", stored)
	}
}

func TestResetClearsAuthAndKeepsJobs(t *testing.T) {
	p, store, authn, _, _ := newTestProcessor(t)
	job := testJob(1)
	submitJob(t, p, "req-1", job)

	response, err := p.HandleRequest(context.Background(), protocol.Request{
		Type:            protocol.RequestReset,
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       "req-reset",
	})
	if err != nil {
		t.Fatalf("HandleRequest(reset): %v", err)
	}
	result, ok := response.(protocol.CommandResult)
	if !ok {
		t.Fatalf("reset response type = %T", response)
	}
	assertValidResponse(t, result)
	if result.Result != "accepted" || result.Status == nil || *result.Status != "reset" || result.JobID != nil {
		t.Fatalf("reset result = %+v", result)
	}
	if authn.resetCalls != 1 || authn.State() != protocol.AuthNotConnected {
		t.Fatalf("auth after reset = %s (calls %d)", authn.State(), authn.resetCalls)
	}
	counts, err := store.Counts()
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Queued != 1 {
		t.Fatalf("reset must keep queued jobs: %+v", counts)
	}
}

func TestResetFailureIsCategoryOnly(t *testing.T) {
	p, _, authn, _, _ := newTestProcessor(t)
	authn.resetErr = errors.New("SECRET-TOKEN-VALUE")

	response, err := p.HandleRequest(context.Background(), protocol.Request{
		Type:            protocol.RequestReset,
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       "req-reset",
	})
	if err != nil {
		t.Fatalf("HandleRequest(reset): %v", err)
	}
	result, ok := response.(protocol.CommandResult)
	if !ok {
		t.Fatalf("reset response type = %T", response)
	}
	assertValidResponse(t, result)
	if result.Result != "rejected" || result.ErrorCategory == nil || *result.ErrorCategory != protocol.ErrorInternal {
		t.Fatalf("reset failure = %+v", result)
	}
	encoded, err := protocol.EncodeResponse(result)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	if bytes.Contains(encoded, []byte("SECRET")) {
		t.Fatal("command result leaked raw error text")
	}
}

func TestStatusPagination(t *testing.T) {
	p, _, _, _, _ := newTestProcessor(t)
	for i := 1; i <= 5; i++ {
		submitJob(t, p, fmt.Sprintf("req-%d", i), testJob(i))
	}

	response, err := p.HandleRequest(context.Background(), statusRequest("status-1", nil, intPtr(2)))
	if err != nil {
		t.Fatalf("HandleRequest(status): %v", err)
	}
	message, ok := response.(protocol.StatusMessage)
	if !ok {
		t.Fatalf("status response type = %T", response)
	}
	assertValidResponse(t, message)
	if message.RequestID == nil || *message.RequestID != "status-1" {
		t.Fatalf("status requestId = %v", message.RequestID)
	}
	if len(message.Jobs) != 2 || message.Jobs[0].JobID != 5 || message.Jobs[1].JobID != 4 {
		t.Fatalf("page 1 = %+v", message.Jobs)
	}
	if message.NextBeforeJobID == nil || *message.NextBeforeJobID != 4 {
		t.Fatalf("page 1 cursor = %v", message.NextBeforeJobID)
	}

	response, err = p.HandleRequest(context.Background(), statusRequest("status-2", message.NextBeforeJobID, intPtr(2)))
	if err != nil {
		t.Fatalf("HandleRequest(status): %v", err)
	}
	message = response.(protocol.StatusMessage)
	assertValidResponse(t, message)
	if len(message.Jobs) != 2 || message.Jobs[0].JobID != 3 || message.Jobs[1].JobID != 2 {
		t.Fatalf("page 2 = %+v", message.Jobs)
	}

	response, err = p.HandleRequest(context.Background(), statusRequest("status-3", message.NextBeforeJobID, intPtr(2)))
	if err != nil {
		t.Fatalf("HandleRequest(status): %v", err)
	}
	message = response.(protocol.StatusMessage)
	assertValidResponse(t, message)
	if len(message.Jobs) != 1 || message.Jobs[0].JobID != 1 || message.NextBeforeJobID != nil {
		t.Fatalf("page 3 = %+v cursor=%v", message.Jobs, message.NextBeforeJobID)
	}
}

func TestConnectStatusEchoesExtensionVersionAndChallenge(t *testing.T) {
	p, _, authn, _, _ := newTestProcessor(t)
	expiresAt := baseTime.Add(15 * time.Minute)
	authn.beginChallenge = &auth.Challenge{
		UserCode:                "ABCD-1234",
		VerificationURI:         "https://github.com/login/device",
		VerificationURIComplete: "https://github.com/login/device?user_code=ABCD-1234",
		ExpiresAt:               expiresAt,
		Interval:                5 * time.Second,
	}
	if err := p.OnConnect(context.Background()); err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	defer p.OnDisconnect()

	response, err := p.HandleRequest(context.Background(), protocol.Request{
		Type:             protocol.RequestConnect,
		ProtocolVersion:  protocol.ProtocolVersion,
		RequestID:        "connect-1",
		ExtensionVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("HandleRequest(connect): %v", err)
	}
	message, ok := response.(protocol.StatusMessage)
	if !ok {
		t.Fatalf("connect response type = %T", response)
	}
	assertValidResponse(t, message)
	if message.ExtensionVersion != "0.1.0" || message.UploaderVersion != DefaultVersion {
		t.Fatalf("versions = %q/%q", message.ExtensionVersion, message.UploaderVersion)
	}
	if message.AuthState != protocol.AuthAuthorizing {
		t.Fatalf("auth state = %s, want authorizing", message.AuthState)
	}
	if message.Authorization.UserCode == nil || *message.Authorization.UserCode != "ABCD-1234" {
		t.Fatalf("user code = %v", message.Authorization.UserCode)
	}
	if message.Authorization.VerificationURI == nil || *message.Authorization.VerificationURI != "https://github.com/login/device" {
		t.Fatalf("verification uri = %v", message.Authorization.VerificationURI)
	}
	// GitHub's device flow returns a query-bearing verification_uri_complete
	// (user_code), which the schema permits and the popup links to directly.
	if message.Authorization.VerificationURIComplete == nil || *message.Authorization.VerificationURIComplete != "https://github.com/login/device?user_code=ABCD-1234" {
		t.Fatalf("complete verification uri = %v", message.Authorization.VerificationURIComplete)
	}
	if message.Authorization.ExpiresAt == nil || *message.Authorization.ExpiresAt != expiresAt.Format(time.RFC3339) {
		t.Fatalf("expires at = %v", message.Authorization.ExpiresAt)
	}
	if message.DrainState != protocol.DrainAuthorizing {
		t.Fatalf("drain state = %s, want authorizing", message.DrainState)
	}
}

func TestRetryCommandEligibility(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	ctx := context.Background()

	retryable := testJob(1)
	submitJob(t, p, "req-1", retryable)
	claimed, err := store.ClaimNext(baseTime)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext: %v, %v", claimed, err)
	}
	if err := store.MarkRetryableError(claimed.ID, string(protocol.ErrorInternal), nil, baseTime.Add(time.Minute), baseTime); err != nil {
		t.Fatalf("MarkRetryableError: %v", err)
	}
	response, err := p.HandleRequest(ctx, retryRequest("retry-1", 1))
	if err != nil {
		t.Fatalf("HandleRequest(retry): %v", err)
	}
	result := response.(protocol.CommandResult)
	assertValidResponse(t, result)
	if result.Result != "accepted" || result.Status == nil || *result.Status != string(protocol.StatusQueued) {
		t.Fatalf("retryable retry = %+v", result)
	}
	if _, err := p.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}

	queued := testJob(2)
	submitJob(t, p, "req-2", queued)
	response, err = p.HandleRequest(ctx, retryRequest("retry-2", 2))
	if err != nil {
		t.Fatalf("HandleRequest(retry): %v", err)
	}
	result = response.(protocol.CommandResult)
	assertValidResponse(t, result)
	if result.Result != "rejected" || result.Status == nil || *result.Status != string(protocol.StatusQueued) ||
		result.ErrorCategory == nil || *result.ErrorCategory != protocol.ErrorIneligibleCommand {
		t.Fatalf("ineligible retry = %+v", result)
	}
	if _, err := p.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}

	response, err = p.HandleRequest(ctx, retryRequest("retry-missing", 9999))
	if err != nil {
		t.Fatalf("HandleRequest(retry missing): %v", err)
	}
	result = response.(protocol.CommandResult)
	assertValidResponse(t, result)
	if result.Result != "rejected" || result.ErrorCategory == nil || *result.ErrorCategory != protocol.ErrorInvalidState {
		t.Fatalf("missing retry = %+v", result)
	}

	conflict := testJob(3)
	submitJob(t, p, "req-3", conflict)
	claimed, err = store.ClaimNext(baseTime)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext: %v, %v", claimed, err)
	}
	if err := store.MarkPermanentConflict(claimed.ID, string(protocol.ErrorInternal), nil, nil, string(protocol.RemoteMissing), baseTime); err != nil {
		t.Fatalf("MarkPermanentConflict: %v", err)
	}
	path := queue.TargetPath(conflict.Kind, conflict.CourseSlug, conflict.LectureNumber)
	publisher.remote[path] = fakeRemote{kind: protocol.RemoteFile, hash: &conflict.ContentHash}
	response, err = p.HandleRequest(ctx, retryRequest("retry-conflict", claimed.ID))
	if err != nil {
		t.Fatalf("HandleRequest(conflict retry): %v", err)
	}
	result = response.(protocol.CommandResult)
	assertValidResponse(t, result)
	if result.Result != "rejected" || result.Status == nil || *result.Status != string(protocol.StatusPermanentConflict) ||
		result.ErrorCategory == nil || *result.ErrorCategory != protocol.ErrorIneligibleCommand {
		t.Fatalf("present conflict retry = %+v", result)
	}

	delete(publisher.remote, path)
	response, err = p.HandleRequest(ctx, retryRequest("retry-conflict-2", claimed.ID))
	if err != nil {
		t.Fatalf("HandleRequest(conflict retry): %v", err)
	}
	result = response.(protocol.CommandResult)
	assertValidResponse(t, result)
	if result.Result != "accepted" || result.Status == nil || *result.Status != string(protocol.StatusQueued) {
		t.Fatalf("removed conflict retry = %+v", result)
	}
}

func TestDiscardCommandEligibility(t *testing.T) {
	p, store, _, _, _ := newTestProcessor(t)
	ctx := context.Background()

	rejected := testJob(1)
	submitJob(t, p, "req-1", rejected)
	claimed, err := store.ClaimNext(baseTime)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext: %v, %v", claimed, err)
	}
	if err := store.MarkRejected(claimed.ID, protocol.StatusRejectedOversized, nil, baseTime); err != nil {
		t.Fatalf("MarkRejected: %v", err)
	}
	response, err := p.HandleRequest(ctx, discardRequest("discard-1", 1))
	if err != nil {
		t.Fatalf("HandleRequest(discard): %v", err)
	}
	result := response.(protocol.CommandResult)
	assertValidResponse(t, result)
	if result.Result != "accepted" || result.Status == nil || *result.Status != "discarded" {
		t.Fatalf("discard = %+v", result)
	}
	if _, err := store.Get(1); !errors.Is(err, queue.ErrJobNotFound) {
		t.Fatalf("discarded job still present: %v", err)
	}

	queued := testJob(2)
	submitJob(t, p, "req-2", queued)
	response, err = p.HandleRequest(ctx, discardRequest("discard-2", 2))
	if err != nil {
		t.Fatalf("HandleRequest(discard): %v", err)
	}
	result = response.(protocol.CommandResult)
	assertValidResponse(t, result)
	if result.Result != "rejected" || result.Status == nil || *result.Status != string(protocol.StatusQueued) ||
		result.ErrorCategory == nil || *result.ErrorCategory != protocol.ErrorIneligibleCommand {
		t.Fatalf("ineligible discard = %+v", result)
	}

	response, err = p.HandleRequest(ctx, discardRequest("discard-missing", 9999))
	if err != nil {
		t.Fatalf("HandleRequest(discard missing): %v", err)
	}
	result = response.(protocol.CommandResult)
	assertValidResponse(t, result)
	if result.Result != "rejected" || result.ErrorCategory == nil || *result.ErrorCategory != protocol.ErrorInvalidState {
		t.Fatalf("missing discard = %+v", result)
	}
}

func TestDrainStateTransitions(t *testing.T) {
	p, _, authn, publisher, clock := newTestProcessor(t)
	ctx := context.Background()

	state, err := p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if state != protocol.DrainIdle {
		t.Fatalf("state with no work = %s, want idle", state)
	}

	authn.mu.Lock()
	authn.state = protocol.AuthAuthorizing
	authn.challenge = &auth.Challenge{
		UserCode:        "ABCD-1234",
		VerificationURI: "https://github.com/login/device",
		ExpiresAt:       baseTime.Add(15 * time.Minute),
		Interval:        5 * time.Second,
	}
	authn.pollStates = []protocol.AuthState{protocol.AuthAuthorizing, protocol.AuthConnected}
	authn.mu.Unlock()

	if p.DrainState() != protocol.DrainAuthorizing {
		t.Fatalf("drain state = %s, want authorizing", p.DrainState())
	}
	state, err = p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce authorizing: %v", err)
	}
	if state != protocol.DrainAuthorizing {
		t.Fatalf("state = %s, want authorizing", state)
	}
	if authn.pollCalls != 1 {
		t.Fatalf("poll calls = %d, want 1", authn.pollCalls)
	}
	state, err = p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce after poll: %v", err)
	}
	if state != protocol.DrainIdle {
		t.Fatalf("state after connect = %s, want idle", state)
	}

	job := testJob(1)
	publisher.publishErr = &github.Error{Category: github.CategoryRetryable, HTTPStatus: 503, Op: "contents.get"}
	submitJob(t, p, "req-1", job)
	state, err = p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce retryable: %v", err)
	}
	if state != protocol.DrainWorking {
		t.Fatalf("state during failure = %s, want working", state)
	}
	state, err = p.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drainOnce backoff: %v", err)
	}
	if state != protocol.DrainWaitingForBackoff || p.DrainState() != protocol.DrainWaitingForBackoff {
		t.Fatalf("state = %s/%s, want waiting_for_backoff", state, p.DrainState())
	}

	publisher.publishErr = nil
	var observed protocol.DrainState
	publisher.onPublish = func() { observed = p.DrainState() }
	clock.Advance(5 * time.Second)
	if _, err := p.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce due: %v", err)
	}
	if observed != protocol.DrainWorking {
		t.Fatalf("state observed during publish = %s, want working", observed)
	}
}

func TestOnConnectProcessesAndDisconnectStops(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	job := testJob(1)
	submitJob(t, p, "req-1", job)

	if err := p.OnConnect(context.Background()); err != nil {
		t.Fatalf("OnConnect: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		stored, err := store.Get(1)
		if err == nil && stored.Status == protocol.StatusUploaded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job was not processed by the drain loop: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.OnDisconnect()
	if publisher.publishCalls == 0 {
		t.Fatal("drain loop never published")
	}

	// A second connect/disconnect cycle must be safe.
	if err := p.OnConnect(context.Background()); err != nil {
		t.Fatalf("second OnConnect: %v", err)
	}
	p.OnDisconnect()
}

func TestInvalidSubmitReturnsRejectionAck(t *testing.T) {
	p, _, _, _, _ := newTestProcessor(t)
	job := testJob(1)
	job.ContentHash = "not-a-hash"

	response, err := p.HandleRequest(context.Background(), submitRequest("req-bad", job))
	if err != nil {
		t.Fatalf("HandleRequest(submit): %v", err)
	}
	ack := response.(protocol.Ack)
	assertValidResponse(t, ack)
	if ack.Status != protocol.AckRejectedInvalidHash {
		t.Fatalf("ack status = %s, want rejected_invalid_hash", ack.Status)
	}
	if ack.ContentHash != nil {
		t.Fatalf("invalid hash must not be echoed: %v", *ack.ContentHash)
	}
	if ack.LectureKey == nil || *ack.LectureKey != job.LectureKey {
		t.Fatalf("valid lecture key should still be echoed: %v", ack.LectureKey)
	}
}

func TestWriteOnceFixtures(t *testing.T) {
	sameHashBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "existing-same-hash.md"))
	if err != nil {
		t.Fatalf("read same-hash fixture: %v", err)
	}
	differentHashBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "existing-different-hash.md"))
	if err != nil {
		t.Fatalf("read different-hash fixture: %v", err)
	}
	malformedBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "malformed-lecture.md"))
	if err != nil {
		t.Fatalf("read malformed fixture: %v", err)
	}

	sameHash, ok := github.ParseTranscriptHash(sameHashBytes)
	if !ok || !protocol.IsValidContentHash(sameHash) {
		t.Fatalf("same-hash fixture hash = %q, ok=%v", sameHash, ok)
	}
	differentHash, ok := github.ParseTranscriptHash(differentHashBytes)
	if !ok || !protocol.IsValidContentHash(differentHash) || differentHash == sameHash {
		t.Fatalf("different-hash fixture hash = %q, ok=%v", differentHash, ok)
	}
	if _, ok := github.ParseTranscriptHash(malformedBytes); ok {
		t.Fatal("malformed fixture must not parse a trusted hash")
	}

	p, store, _, publisher, _ := newTestProcessor(t)
	ctx := context.Background()

	unchangedJob := testJob(1)
	unchangedJob.ContentHash = sameHash
	publisher.remote[queue.TargetPath(unchangedJob.Kind, unchangedJob.CourseSlug, unchangedJob.LectureNumber)] = fakeRemote{kind: protocol.RemoteFile, hash: &sameHash}
	publisher.remote[queue.TimestampedPath(unchangedJob.Kind, unchangedJob.CourseSlug, unchangedJob.LectureNumber)] = fakeRemote{kind: protocol.RemoteFile, hash: &sameHash}
	submitJob(t, p, "req-same", unchangedJob)
	if _, err := p.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce same: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusUnchanged {
		t.Fatalf("same-hash status = %s, want unchanged", stored.Status)
	}

	conflictJob := testJob(2)
	conflictJob.ContentHash = testHash("conflict-local")
	publisher.remote[queue.TargetPath(conflictJob.Kind, conflictJob.CourseSlug, conflictJob.LectureNumber)] = fakeRemote{kind: protocol.RemoteFile, hash: &differentHash}
	submitJob(t, p, "req-different", conflictJob)
	if _, err := p.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce different: %v", err)
	}
	stored, err = store.Get(2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusPermanentConflict {
		t.Fatalf("different-hash status = %s, want permanent_conflict", stored.Status)
	}

	malformedJob := testJob(3)
	publisher.remote[queue.TargetPath(malformedJob.Kind, malformedJob.CourseSlug, malformedJob.LectureNumber)] = fakeRemote{kind: protocol.RemoteMalformed}
	submitJob(t, p, "req-malformed", malformedJob)
	if _, err := p.drainOnce(ctx); err != nil {
		t.Fatalf("drainOnce malformed: %v", err)
	}
	stored, err = store.Get(3)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusPermanentConflict || stored.RemoteFileKind == nil || *stored.RemoteFileKind != string(protocol.RemoteMalformed) {
		t.Fatalf("malformed status = %s kind=%v", stored.Status, stored.RemoteFileKind)
	}
}

func TestPermissionErrorMarksRejectedPermission(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	publisher.publishErr = &github.Error{Category: github.CategoryPermission, HTTPStatus: 403, Op: "contents.put"}

	submitJob(t, p, "req-1", testJob(1))
	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusRejectedPermission || stored.AttemptCount != 1 {
		t.Fatalf("permission job = %+v", stored)
	}
	if stored.LastErrorHTTPStatus == nil || *stored.LastErrorHTTPStatus != 403 {
		t.Fatalf("http status = %v", stored.LastErrorHTTPStatus)
	}
}

func TestAuthErrorPausesDrainAndSchedulesRetry(t *testing.T) {
	p, store, authn, publisher, _ := newTestProcessor(t)
	publisher.publishErr = &github.Error{Category: github.CategoryAuth, HTTPStatus: 401, Op: "contents.put"}

	submitJob(t, p, "req-1", testJob(1))
	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusRetryableError {
		t.Fatalf("auth failure status = %s, want retryable_error", stored.Status)
	}
	if stored.LastErrorCategory == nil || *stored.LastErrorCategory != string(protocol.ErrorReauthorizationRequired) {
		t.Fatalf("error category = %v", stored.LastErrorCategory)
	}

	authn.mu.Lock()
	authn.state = protocol.AuthReauthorizationRequired
	authn.mu.Unlock()
	state, err := p.drainOnce(context.Background())
	if err != nil {
		t.Fatalf("drainOnce paused: %v", err)
	}
	if state != protocol.DrainWaitingForBackoff {
		t.Fatalf("state while paused = %s, want waiting_for_backoff", state)
	}
	if publisher.publishCalls != 1 {
		t.Fatalf("paused queue must not claim: publish calls = %d", publisher.publishCalls)
	}
}

func TestStatusRequestBeforeConnectUsesUnknownVersion(t *testing.T) {
	p, _, _, _, _ := newTestProcessor(t)
	response, err := p.HandleRequest(context.Background(), statusRequest("status-1", nil, nil))
	if err != nil {
		t.Fatalf("HandleRequest(status): %v", err)
	}
	message := response.(protocol.StatusMessage)
	assertValidResponse(t, message)
	if message.ExtensionVersion != unknownExtensionVersion {
		t.Fatalf("extension version = %q, want %q", message.ExtensionVersion, unknownExtensionVersion)
	}
	if message.Jobs == nil {
		t.Fatal("jobs must be a non-nil empty list")
	}
}

func TestResponseTextNeverCarriesRawErrors(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	secret := "raw-remote-body-SECRET"
	publisher.publishErr = &github.Error{Category: github.CategoryRetryable, HTTPStatus: 500, Op: "contents.get"}
	submitJob(t, p, "req-1", testJob(1))
	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	summary, err := json.Marshal(stored.Summary())
	if err != nil {
		t.Fatalf("Marshal(summary): %v", err)
	}
	if bytes.Contains(summary, []byte(secret)) {
		t.Fatal("job summary leaked raw error text")
	}
	if strings.Contains(string(stored.Summary().Status), secret) {
		t.Fatal("status string leaked raw error text")
	}
}

func TestHostSessionEndToEnd(t *testing.T) {
	p, _, _, _, _ := newTestProcessor(t)
	extensionID := strings.Repeat("a", 32)
	origin, err := host.ExtensionOrigin(extensionID)
	if err != nil {
		t.Fatalf("ExtensionOrigin: %v", err)
	}

	input := &bytes.Buffer{}
	writeFrame := func(request protocol.Request) {
		t.Helper()
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("Marshal request: %v", err)
		}
		if err := host.WriteFrame(input, payload); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}
	writeFrame(protocol.Request{
		Type:             protocol.RequestConnect,
		ProtocolVersion:  protocol.ProtocolVersion,
		RequestID:        "connect-1",
		ExtensionVersion: "0.1.0",
	})
	job := testJob(1)
	writeFrame(submitRequest("submit-1", job))
	writeFrame(statusRequest("status-1", nil, nil))
	writeFrame(protocol.Request{
		Type:            protocol.RequestReset,
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       "reset-1",
	})

	output := &bytes.Buffer{}
	server := host.NewServer(input, output, p, p, origin, origin)
	if err := server.Run(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Server.Run error = %v, want io.EOF", err)
	}

	var responses []any
	for {
		payload, err := host.ReadFrame(output)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		decoded, err := protocol.DecodeResponse(payload)
		if err != nil {
			t.Fatalf("DecodeResponse: %v", err)
		}
		responses = append(responses, decoded)
	}
	if len(responses) != 4 {
		t.Fatalf("responses = %d, want 4: %#v", len(responses), responses)
	}
	if _, ok := responses[0].(protocol.StatusMessage); !ok {
		t.Fatalf("connect response = %T, want status", responses[0])
	}
	ack, ok := responses[1].(protocol.Ack)
	if !ok || ack.Status != protocol.AckQueued {
		t.Fatalf("submit response = %#v", responses[1])
	}
	if _, ok := responses[2].(protocol.StatusMessage); !ok {
		t.Fatalf("status response = %T, want status", responses[2])
	}
	result, ok := responses[3].(protocol.CommandResult)
	if !ok || result.Result != "accepted" || result.Status == nil || *result.Status != "reset" {
		t.Fatalf("reset response = %#v", responses[3])
	}
}

func retryRequest(requestID string, jobID int64) protocol.Request {
	return protocol.Request{
		Type:            protocol.RequestRetryJob,
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       requestID,
		JobID:           &jobID,
	}
}

func discardRequest(requestID string, jobID int64) protocol.Request {
	return protocol.Request{
		Type:            protocol.RequestDiscardJob,
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       requestID,
		JobID:           &jobID,
		Confirmation:    "discard",
	}
}

func TestPlainOnlyPublishesSingleFile(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	job := testJob(1)
	job.TimestampedTranscript = ""
	submitJob(t, p, "req-plain", job)

	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusUploaded {
		t.Fatalf("status = %s, want uploaded", stored.Status)
	}
	if publisher.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", publisher.createCalls)
	}
	if _, ok := publisher.contents[queue.TargetPath(job.Kind, job.CourseSlug, job.LectureNumber)]; !ok {
		t.Fatalf("plain-only content missing at %s", queue.TargetPath(job.Kind, job.CourseSlug, job.LectureNumber))
	}
	if _, ok := publisher.contents[queue.TimestampedPath(job.Kind, job.CourseSlug, job.LectureNumber)]; ok {
		t.Fatal("plain-only job must not publish a timestamped file")
	}
	if stored.TargetPath() != queue.TargetPath(job.Kind, job.CourseSlug, job.LectureNumber) {
		t.Fatalf("target path = %q", stored.TargetPath())
	}
}

func TestDiscussionPublishesToDiscussionsFolder(t *testing.T) {
	p, store, _, publisher, _ := newTestProcessor(t)
	job := testJob(1)
	job.Kind = protocol.KindDiscussion
	submitJob(t, p, "req-discussion", job)

	if _, err := p.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	stored, err := store.Get(1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != protocol.StatusUploaded {
		t.Fatalf("status = %s, want uploaded", stored.Status)
	}
	if publisher.createCalls != 2 {
		t.Fatalf("create calls = %d, want 2", publisher.createCalls)
	}
	plainPath := queue.TargetPath(protocol.KindDiscussion, job.CourseSlug, job.LectureNumber)
	timestampedPath := queue.TimestampedPath(protocol.KindDiscussion, job.CourseSlug, job.LectureNumber)
	if plainPath != "eecs491/discussions/001.md" || timestampedPath != "eecs491/discussions/timestamped/001.md" {
		t.Fatalf("discussion paths = %q / %q", plainPath, timestampedPath)
	}
	if _, ok := publisher.contents[plainPath]; !ok {
		t.Fatalf("missing discussion plain content at %s", plainPath)
	}
	if _, ok := publisher.contents[timestampedPath]; !ok {
		t.Fatalf("missing discussion timestamped content at %s", timestampedPath)
	}
	if stored.TargetPath() != plainPath {
		t.Fatalf("stored target path = %q, want %q", stored.TargetPath(), plainPath)
	}
}
