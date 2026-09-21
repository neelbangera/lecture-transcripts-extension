// Package processor connects the durable queue, GitHub authorization, Markdown
// rendering, and write-once publishing into one serial drain.  It owns every
// job transition and is the only component that answers Native Messaging
// requests with queue decisions; the host transport merely frames and
// validates the closed responses produced here.
package processor

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/auth"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/github"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/logging"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/markdown"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/queue"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/retry"
)

const (
	// DefaultVersion is used until scripts/build-uploader.sh injects the real
	// build version with -ldflags "-X main.version=...".
	DefaultVersion = "dev"

	// unknownExtensionVersion keeps a status response valid when the
	// extension sends status_request before it has sent connect.
	unknownExtensionVersion = "unknown"

	defaultRetryPollInterval = time.Second
	defaultStatusPageLimit   = 50
)

// AuthManager is the narrow authorization surface the processor needs.  The
// auth.Manager implements it; tests substitute a fake so no test ever touches
// the Keychain.
type AuthManager interface {
	State() protocol.AuthState
	Challenge() *auth.Challenge
	Begin(ctx context.Context) (*auth.Challenge, error)
	Poll(ctx context.Context) (protocol.AuthState, error)
	Reset() error
}

// Publisher is the narrow write-once publishing surface.  *github.Client
// implements it; tests substitute an in-memory fake.
type Publisher interface {
	InspectFile(ctx context.Context, path string) (github.RemoteFile, error)
	Publish(ctx context.Context, path string, job protocol.TranscriptJob, content []byte) (github.PublishResult, error)
}

// Config assembles a Processor.  Store, Auth, and Publisher are required.
type Config struct {
	Store     *queue.Store
	Auth      AuthManager
	Publisher Publisher
	Backoff   retry.Backoff
	Logger    *logging.Logger
	Version   string
	Now       func() time.Time
	Lease     time.Duration

	// RetryPollInterval bounds how long a waiting_for_backoff drain sleeps
	// before checking whether a durable retry became due.
	RetryPollInterval time.Duration
}

// Processor serially claims queued jobs, publishes them write-once, and
// records the durable outcome.  It also implements the host RequestHandler
// and SessionLifecycle interfaces.
type Processor struct {
	store     *queue.Store
	auth      AuthManager
	publisher Publisher
	backoff   retry.Backoff
	logger    *logging.Logger
	version   string
	now       func() time.Time
	lease     time.Duration

	retryPollInterval time.Duration

	wake chan struct{}

	mu               sync.Mutex
	extensionVersion string
	processing       bool
	polling          bool
	running          bool
	stopping         bool
	stop             chan struct{}
	done             chan struct{}
}

// New validates the composition and returns a Processor.  No network,
// Keychain, or filesystem access happens here.
func New(cfg Config) (*Processor, error) {
	if cfg.Store == nil {
		return nil, errors.New("processor: queue store is required")
	}
	if cfg.Auth == nil {
		return nil, errors.New("processor: auth manager is required")
	}
	if cfg.Publisher == nil {
		return nil, errors.New("processor: publisher is required")
	}
	processor := &Processor{
		store:             cfg.Store,
		auth:              cfg.Auth,
		publisher:         cfg.Publisher,
		backoff:           cfg.Backoff,
		logger:            cfg.Logger,
		version:           cfg.Version,
		now:               cfg.Now,
		lease:             cfg.Lease,
		retryPollInterval: cfg.RetryPollInterval,
		wake:              make(chan struct{}, 1),
	}
	if len(processor.backoff.Schedule()) == 0 {
		processor.backoff = retry.New()
	}
	if processor.version == "" {
		processor.version = DefaultVersion
	}
	if processor.now == nil {
		processor.now = time.Now
	}
	if processor.lease <= 0 {
		processor.lease = config.UploadLease
	}
	if processor.retryPollInterval <= 0 {
		processor.retryPollInterval = defaultRetryPollInterval
	}
	return processor, nil
}

// OnConnect recovers startup state, begins or resumes authorization, and
// starts the single drain goroutine for this Native Messaging session.
// Authorization failures are exposed through status instead of failing the
// connect request.
func (p *Processor) OnConnect(ctx context.Context) error {
	now := p.now()
	if recovered, err := p.store.RecoverStaleLeases(now, p.lease); err != nil {
		p.logError(logging.EventLeaseRecovered, err)
	} else if recovered > 0 {
		p.logEvent(logging.EventLeaseRecovered, "")
	}
	if promoted, err := p.store.PromoteDueRetries(now); err != nil {
		p.logError(logging.EventRetryPromoted, err)
	} else if promoted > 0 {
		p.logEvent(logging.EventRetryPromoted, "")
	}

	if _, err := p.auth.Begin(ctx); err != nil {
		p.logError(logging.EventAuthState, err)
	}
	p.logStatus(logging.EventAuthState, string(p.auth.State()))

	p.mu.Lock()
	if p.running || p.stopping {
		p.mu.Unlock()
		p.wakeDrain()
		return nil
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	p.running = true
	p.stop = stop
	p.done = done
	p.mu.Unlock()

	go p.run(stop, done)
	return nil
}

// OnDisconnect stops the drain after any in-flight job finishes and releases
// the session channels.  A port close therefore never abandons a job in the
// middle of a publish.
func (p *Processor) OnDisconnect() {
	p.mu.Lock()
	if !p.running || p.stopping {
		p.mu.Unlock()
		return
	}
	p.stopping = true
	stop, done := p.stop, p.done
	p.mu.Unlock()

	close(stop)
	<-done

	p.mu.Lock()
	p.running = false
	p.stopping = false
	p.processing = false
	p.polling = false
	p.mu.Unlock()
}

// HandleRequest implements host.RequestHandler.  It returns only values that
// protocol.EncodeResponse validates; internal failures become category-only
// ProtocolErrors.
func (p *Processor) HandleRequest(ctx context.Context, request protocol.Request) (any, error) {
	switch request.Type {
	case protocol.RequestConnect:
		p.mu.Lock()
		p.extensionVersion = request.ExtensionVersion
		p.mu.Unlock()
		return p.statusMessage(&request.RequestID, request.ExtensionVersion, nil, defaultStatusPageLimit)
	case protocol.RequestSubmitJob:
		return p.handleSubmit(request)
	case protocol.RequestStatus:
		return p.statusMessage(&request.RequestID, p.currentExtensionVersion(), request.BeforeJobID, request.EffectiveLimit())
	case protocol.RequestRetryJob:
		return p.handleRetry(ctx, request)
	case protocol.RequestDiscardJob:
		return p.handleDiscard(request)
	case protocol.RequestReset:
		return p.handleReset(request)
	default:
		return nil, protocol.NewProtocolError(protocol.ErrorRejectedInvalidSchema, false)
	}
}

// DrainState reports the closed drain vocabulary.  working means a job is
// actively processing or due, authorizing means device-code polling is
// active, waiting_for_backoff means only future retries remain, and idle
// means no active or future-due work exists.
func (p *Processor) DrainState() protocol.DrainState {
	p.mu.Lock()
	processing, polling := p.processing, p.polling
	p.mu.Unlock()
	if processing {
		return protocol.DrainWorking
	}
	if polling || p.auth.State() == protocol.AuthAuthorizing {
		return protocol.DrainAuthorizing
	}
	state, err := p.quiescentState()
	if err != nil {
		return protocol.DrainIdle
	}
	return state
}

func (p *Processor) run(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for {
		select {
		case <-stop:
			// A port close stops the drain after the in-flight job finishes
			// rather than claiming more work for a session that is gone.
			return
		default:
		}
		state, err := p.drainOnce(ctx)
		if err != nil {
			p.logError(logging.EventProtocolError, err)
		}
		switch state {
		case protocol.DrainIdle:
			// The timer is a safety net: it lets a later drainOnce recover a
			// lease whose owner died without a wake-up arriving.
			if !p.wait(ctx, stop, time.After(p.retryPollInterval)) {
				return
			}
		case protocol.DrainWaitingForBackoff:
			if !p.wait(ctx, stop, time.After(p.retryPollInterval)) {
				return
			}
		case protocol.DrainAuthorizing:
			if !p.wait(ctx, stop, time.After(p.pollDelay())) {
				return
			}
		}
	}
}

func (p *Processor) wait(ctx context.Context, stop <-chan struct{}, timer <-chan time.Time) bool {
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	case <-p.wake:
		return true
	case <-timer:
		return true
	}
}

// drainOnce performs one serial unit of work and reports the resulting drain
// state.  Tests drive this directly with a fake clock so no timing depends on
// wall-clock sleeps.
func (p *Processor) drainOnce(ctx context.Context) (protocol.DrainState, error) {
	now := p.now()
	if promoted, err := p.store.PromoteDueRetries(now); err != nil {
		return protocol.DrainIdle, err
	} else if promoted > 0 {
		p.logEvent(logging.EventRetryPromoted, "")
	}
	if recovered, err := p.store.RecoverStaleLeases(now, p.lease); err != nil {
		return protocol.DrainIdle, err
	} else if recovered > 0 {
		p.logEvent(logging.EventLeaseRecovered, "")
	}

	authState := p.auth.State()
	if authState == protocol.AuthAuthorizing {
		p.setPolling(true)
		state, err := p.auth.Poll(ctx)
		p.setPolling(false)
		if err != nil {
			p.logError(logging.EventAuthState, err)
		} else {
			p.logStatus(logging.EventAuthState, string(state))
		}
		if state == protocol.AuthAuthorizing {
			return protocol.DrainAuthorizing, nil
		}
		authState = state
	}
	if authState != protocol.AuthConnected {
		return p.quiescentState()
	}

	job, err := p.store.ClaimNext(now)
	if err != nil {
		return protocol.DrainWorking, err
	}
	if job == nil {
		return p.quiescentState()
	}

	p.setProcessing(true)
	processErr := p.processJob(ctx, job)
	p.setProcessing(false)
	if processErr != nil {
		return protocol.DrainWorking, processErr
	}
	return protocol.DrainWorking, nil
}

func (p *Processor) quiescentState() (protocol.DrainState, error) {
	counts, err := p.store.Counts()
	if err != nil {
		return protocol.DrainIdle, err
	}
	if counts.Queued > 0 && p.auth.State() == protocol.AuthConnected {
		return protocol.DrainWorking, nil
	}
	if counts.RetryableError > 0 {
		if p.auth.State() == protocol.AuthConnected {
			due, err := p.hasDueRetry(p.now())
			if err != nil {
				return protocol.DrainIdle, err
			}
			if due {
				return protocol.DrainWorking, nil
			}
		}
		return protocol.DrainWaitingForBackoff, nil
	}
	return protocol.DrainIdle, nil
}

// hasDueRetry inspects one bounded status page so a retry whose next_attempt_at
// has already passed is never reported as waiting_for_backoff.  The drain loop
// promotes it on its next poll; this keeps the port-close contract honest in
// the interval between the due time and that poll.
func (p *Processor) hasDueRetry(now time.Time) (bool, error) {
	jobs, _, err := p.store.StatusPage(nil, defaultStatusPageLimit)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.Status != protocol.StatusRetryableError || job.NextAttemptAt == nil {
			continue
		}
		dueAt, parseErr := time.Parse(time.RFC3339, *job.NextAttemptAt)
		if parseErr != nil {
			continue
		}
		if !dueAt.After(now) {
			return true, nil
		}
	}
	return false, nil
}

func (p *Processor) processJob(ctx context.Context, job *queue.Job) error {
	p.logEvent(logging.EventJobClaimed, job.LectureKey)
	content := markdown.Render(job.Payload)
	result, err := p.publisher.Publish(ctx, job.TargetPath(), job.Payload, content)
	now := p.now()
	if err != nil {
		return p.handlePublishError(job, err, now)
	}
	switch result.Outcome {
	case github.OutcomeCreated:
		if err := p.store.MarkUploaded(job.ID, now); err != nil {
			return err
		}
		p.logEvent(logging.EventJobUploaded, job.LectureKey)
	case github.OutcomeUnchanged:
		remoteHash := job.ContentHash
		if result.Remote.ContentHash != nil && protocol.IsValidContentHash(*result.Remote.ContentHash) {
			remoteHash = *result.Remote.ContentHash
		}
		if err := p.store.MarkUnchanged(job.ID, remoteHash, now); err != nil {
			return err
		}
		p.logEvent(logging.EventJobUnchanged, job.LectureKey)
	case github.OutcomeConflict:
		return p.markConflict(job, result.Remote, result.HTTPStatus, now)
	default:
		return p.scheduleRetry(job, protocol.ErrorInternal, nil, now)
	}
	return nil
}

func (p *Processor) handlePublishError(job *queue.Job, err error, now time.Time) error {
	var githubErr *github.Error
	if !errors.As(err, &githubErr) {
		return p.scheduleRetry(job, protocol.ErrorInternal, nil, now)
	}
	status := httpStatusPointer(githubErr.HTTPStatus)
	switch githubErr.Category {
	case github.CategoryAuth:
		if scheduleErr := p.scheduleRetry(job, protocol.ErrorReauthorizationRequired, status, now); scheduleErr != nil {
			return scheduleErr
		}
		p.logStatus(logging.EventAuthState, string(p.auth.State()))
		return nil
	case github.CategoryPermission:
		if rejectErr := p.store.MarkRejectedPermission(job.ID, status, now); rejectErr != nil {
			return rejectErr
		}
		p.logEvent(logging.EventJobRejected, job.LectureKey)
		return nil
	case github.CategoryRetryable, github.CategoryRateLimit:
		return p.scheduleRetry(job, githubErr.Category.ProtocolCategory(), status, now)
	default:
		return p.markConflict(job, github.RemoteFile{Kind: protocol.RemoteMalformed}, status, now)
	}
}

func (p *Processor) markConflict(job *queue.Job, remote github.RemoteFile, httpStatus *int, now time.Time) error {
	kind := remote.Kind
	if kind == "" {
		kind = protocol.RemoteMalformed
	}
	if err := p.store.MarkPermanentConflict(job.ID, string(github.CategoryPermanent.ProtocolCategory()), httpStatus, remote.ContentHash, string(kind), now); err != nil {
		return err
	}
	p.logEvent(logging.EventJobPermanentConflict, job.LectureKey)
	return nil
}

// scheduleRetry persists the pre-increment attempt count with the backoff
// delay for that attempt.  MarkRetryableError increments attempt_count, so
// the first failure uses index 0 (5s) exactly as the shared schedule defines.
func (p *Processor) scheduleRetry(job *queue.Job, category protocol.ErrorCategory, httpStatus *int, now time.Time) error {
	nextAttemptAt := p.backoff.NextAttemptAt(job.AttemptCount, now)
	if err := p.store.MarkRetryableError(job.ID, string(category), httpStatus, nextAttemptAt, now); err != nil {
		return err
	}
	p.logEvent(logging.EventJobRetryableError, job.LectureKey)
	return nil
}

func (p *Processor) handleSubmit(request protocol.Request) (any, error) {
	if request.Job == nil {
		return nil, protocol.NewProtocolError(protocol.ErrorRejectedInvalidSchema, false)
	}
	result, err := p.store.Enqueue(*request.Job, p.now())
	if err != nil {
		var protocolErr protocol.ProtocolError
		if errors.As(err, &protocolErr) {
			if status, ok := submitAckStatus(protocolErr.Category); ok {
				return rejectedSubmitAck(request, status), nil
			}
		}
		return nil, protocol.NewProtocolError(protocol.ErrorInternal, false)
	}

	ack := protocol.Ack{
		Type:            "ack",
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       request.RequestID,
		Operation:       "submit",
	}
	lectureKey := request.Job.LectureKey
	contentHash := request.Job.ContentHash
	ack.LectureKey = &lectureKey
	ack.ContentHash = &contentHash

	switch result.Outcome {
	case queue.EnqueueQueued:
		jobID := result.JobID
		ack.JobID = &jobID
		ack.Status = protocol.AckQueued
		p.logEvent(logging.EventJobEnqueued, lectureKey)
		p.wakeDrain()
	case queue.EnqueueDuplicate:
		jobID := result.JobID
		ack.JobID = &jobID
		ack.Status = protocol.AckAlreadyQueued
		existing := result.ExistingStatus
		ack.ExistingStatus = &existing
		p.logEvent(logging.EventJobDuplicate, lectureKey)
	case queue.EnqueueDuplicateTerminal:
		jobID := result.JobID
		ack.JobID = &jobID
		ack.Status = protocol.AckRejectedDuplicateTerminal
		existing := result.ExistingStatus
		ack.ExistingStatus = &existing
		ack.Action = result.Action
		p.logEvent(logging.EventJobDuplicate, lectureKey)
	case queue.EnqueueRejectedQueueFull:
		ack.Status = protocol.AckRejectedQueueFull
		p.logEvent(logging.EventJobQueueFull, lectureKey)
	default:
		return nil, protocol.NewProtocolError(protocol.ErrorInternal, false)
	}
	return ack, nil
}

func (p *Processor) handleRetry(ctx context.Context, request protocol.Request) (any, error) {
	if request.JobID == nil {
		return nil, protocol.NewProtocolError(protocol.ErrorRejectedInvalidSchema, false)
	}
	jobID := *request.JobID
	job, err := p.store.Get(jobID)
	if err != nil {
		if errors.Is(err, queue.ErrJobNotFound) {
			return rejectedCommand(request.RequestID, "retry", &jobID, nil, protocol.ErrorInvalidState), nil
		}
		return nil, protocol.NewProtocolError(protocol.ErrorInternal, false)
	}

	if job.Status == protocol.StatusPermanentConflict {
		remote, inspectErr := p.publisher.InspectFile(ctx, job.TargetPath())
		if inspectErr != nil {
			category := commandErrorCategory(inspectErr)
			return rejectedCommand(request.RequestID, "retry", &jobID, &job.Status, category), nil
		}
		if remote.Kind != protocol.RemoteMissing {
			return rejectedCommand(request.RequestID, "retry", &jobID, &job.Status, protocol.ErrorIneligibleCommand), nil
		}
	}

	status, err := p.store.RetryJob(jobID, p.now())
	if err != nil {
		switch {
		case errors.Is(err, queue.ErrNotEligible):
			current := status
			if current == "" {
				current = job.Status
			}
			return rejectedCommand(request.RequestID, "retry", &jobID, &current, protocol.ErrorIneligibleCommand), nil
		case errors.Is(err, queue.ErrJobNotFound):
			return rejectedCommand(request.RequestID, "retry", &jobID, nil, protocol.ErrorInvalidState), nil
		default:
			return nil, protocol.NewProtocolError(protocol.ErrorInternal, false)
		}
	}
	p.wakeDrain()
	return acceptedCommand(request.RequestID, "retry", &jobID, string(status)), nil
}

func (p *Processor) handleDiscard(request protocol.Request) (any, error) {
	if request.JobID == nil {
		return nil, protocol.NewProtocolError(protocol.ErrorRejectedInvalidSchema, false)
	}
	jobID := *request.JobID
	status, err := p.store.DiscardJob(jobID, p.now())
	if err != nil {
		switch {
		case errors.Is(err, queue.ErrNotEligible):
			current := status
			return rejectedCommand(request.RequestID, "discard", &jobID, &current, protocol.ErrorIneligibleCommand), nil
		case errors.Is(err, queue.ErrJobNotFound):
			return rejectedCommand(request.RequestID, "discard", &jobID, nil, protocol.ErrorInvalidState), nil
		default:
			return nil, protocol.NewProtocolError(protocol.ErrorInternal, false)
		}
	}
	return acceptedCommand(request.RequestID, "discard", &jobID, "discarded"), nil
}

func (p *Processor) handleReset(request protocol.Request) (any, error) {
	if err := p.auth.Reset(); err != nil {
		p.logError(logging.EventAuthState, err)
		return rejectedCommand(request.RequestID, "reset", nil, nil, protocol.ErrorInternal), nil
	}
	p.logStatus(logging.EventAuthState, string(protocol.AuthNotConnected))
	p.wakeDrain()
	return acceptedCommand(request.RequestID, "reset", nil, "reset"), nil
}

func (p *Processor) statusMessage(requestID *string, extensionVersion string, before *int64, limit int) (protocol.StatusMessage, error) {
	counts, err := p.store.Counts()
	if err != nil {
		return protocol.StatusMessage{}, protocol.NewProtocolError(protocol.ErrorInternal, false)
	}
	jobs, next, err := p.store.StatusPage(before, limit)
	if err != nil {
		return protocol.StatusMessage{}, protocol.NewProtocolError(protocol.ErrorInternal, false)
	}
	if extensionVersion == "" {
		extensionVersion = unknownExtensionVersion
	}
	return protocol.StatusMessage{
		Type:             "status",
		ProtocolVersion:  protocol.ProtocolVersion,
		RequestID:        requestID,
		ExtensionVersion: extensionVersion,
		UploaderVersion:  p.version,
		AuthState:        p.auth.State(),
		Authorization:    p.authorization(),
		DrainState:       p.DrainState(),
		Counts:           counts,
		Jobs:             jobs,
		NextBeforeJobID:  next,
	}, nil
}

// authorization maps the active device challenge into the closed status
// shape.  Values that the protocol validator would reject (for example a
// verification_uri_complete carrying the documented ?user_code= query) are
// omitted rather than emitted as an invalid status frame; the popup falls
// back to verification_uri plus userCode.
func (p *Processor) authorization() protocol.Authorization {
	challenge := p.auth.Challenge()
	if challenge == nil {
		return protocol.Authorization{}
	}
	authorization := protocol.Authorization{}
	if printableBounded(challenge.UserCode, 64) {
		value := challenge.UserCode
		authorization.UserCode = &value
	}
	if value, ok := safeGitHubURL(challenge.VerificationURI); ok {
		authorization.VerificationURI = &value
	}
	if value, ok := safeGitHubURL(challenge.VerificationURIComplete); ok {
		authorization.VerificationURIComplete = &value
	}
	if !challenge.ExpiresAt.IsZero() {
		value := challenge.ExpiresAt.UTC().Truncate(time.Second).Format(time.RFC3339)
		authorization.ExpiresAt = &value
	}
	return authorization
}

func (p *Processor) currentExtensionVersion() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.extensionVersion == "" {
		return unknownExtensionVersion
	}
	return p.extensionVersion
}

func (p *Processor) setProcessing(value bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.processing = value
}

func (p *Processor) setPolling(value bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.polling = value
}

func (p *Processor) wakeDrain() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Processor) pollDelay() time.Duration {
	challenge := p.auth.Challenge()
	if challenge != nil && challenge.Interval > 0 {
		return challenge.Interval
	}
	return auth.DefaultPollInterval
}

func (p *Processor) logEvent(event logging.Event, lectureKey string) {
	if p.logger == nil {
		return
	}
	p.logger.Info(event, logging.Fields{LectureKey: lectureKey})
}

func (p *Processor) logStatus(event logging.Event, status string) {
	if p.logger == nil {
		return
	}
	p.logger.Info(event, logging.Fields{Status: status})
}

func (p *Processor) logError(event logging.Event, err error) {
	if p.logger == nil {
		return
	}
	p.logger.Warn(event, logging.Fields{ErrorCategory: string(protocol.ErrorInternal)})
}

func rejectedSubmitAck(request protocol.Request, status protocol.AckStatus) protocol.Ack {
	ack := protocol.Ack{
		Type:            "ack",
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       request.RequestID,
		Operation:       "submit",
		Status:          status,
	}
	if request.Job == nil {
		return ack
	}
	if protocol.IsValidLectureKey(request.Job.LectureKey) {
		value := request.Job.LectureKey
		ack.LectureKey = &value
	}
	if protocol.IsValidContentHash(request.Job.ContentHash) {
		value := request.Job.ContentHash
		ack.ContentHash = &value
	}
	return ack
}

func submitAckStatus(category protocol.ErrorCategory) (protocol.AckStatus, bool) {
	switch category {
	case protocol.ErrorRejectedInvalidSchema, protocol.ErrorRejectedUnknownField,
		protocol.ErrorRejectedOversized, protocol.ErrorRejectedInvalidHash,
		protocol.ErrorRejectedUnsafeURL, protocol.ErrorRejectedQueueFull:
		return protocol.AckStatus(category), true
	default:
		return "", false
	}
}

func acceptedCommand(requestID, operation string, jobID *int64, status string) protocol.CommandResult {
	value := status
	return protocol.CommandResult{
		Type:            "command_result",
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       requestID,
		Operation:       operation,
		JobID:           jobID,
		Result:          "accepted",
		Status:          &value,
	}
}

func rejectedCommand(requestID, operation string, jobID *int64, status *protocol.QueueStatus, category protocol.ErrorCategory) protocol.CommandResult {
	value := category
	result := protocol.CommandResult{
		Type:            "command_result",
		ProtocolVersion: protocol.ProtocolVersion,
		RequestID:       requestID,
		Operation:       operation,
		JobID:           jobID,
		Result:          "rejected",
		ErrorCategory:   &value,
	}
	if status != nil {
		text := string(*status)
		result.Status = &text
	}
	return result
}

func commandErrorCategory(err error) protocol.ErrorCategory {
	var githubErr *github.Error
	if errors.As(err, &githubErr) {
		switch githubErr.Category {
		case github.CategoryAuth:
			return protocol.ErrorReauthorizationRequired
		case github.CategoryPermission:
			return protocol.ErrorRejectedPermission
		}
	}
	return protocol.ErrorInternal
}

func httpStatusPointer(status int) *int {
	if status < 100 || status > 599 {
		return nil
	}
	value := status
	return &value
}

func safeGitHubURL(raw string) (string, bool) {
	if raw == "" || len(raw) > 2048 {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "github.com") ||
		parsed.User != nil {
		return "", false
	}
	return raw, true
}

func printableBounded(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}
