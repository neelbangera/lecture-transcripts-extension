package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

const (
	// DefaultDeviceCodeURL is the GitHub App device-code endpoint.
	DefaultDeviceCodeURL = "https://github.com/login/device/code"
	// DefaultTokenURL is the GitHub App token endpoint.
	DefaultTokenURL = "https://github.com/login/oauth/access_token"
	// DefaultAPIBaseURL is the GitHub REST API used for the connect sanity
	// checks.
	DefaultAPIBaseURL = "https://api.github.com"
	// DefaultRefreshThreshold refreshes an access token with 5 minutes or
	// less remaining.
	DefaultRefreshThreshold = 5 * time.Minute
	// DefaultPollInterval is used only when GitHub omits the interval.
	DefaultPollInterval = 5 * time.Second
	// SlowDownIncrement is used when a slow_down response omits the new
	// interval.
	slowDownIncrement = 5 * time.Second

	authRequestTimeout   = 30 * time.Second
	maxAuthResponseBytes = 64 << 10
	deviceGrantType      = "urn:ietf:params:oauth:grant-type:device_code"
	refreshGrantType     = "refresh_token"
	githubAPIVersion     = "2026-03-10"
	githubAccept         = "application/vnd.github+json"
)

var (
	errInvalidAuthResponse = errors.New("invalid github authorization response")
	errTransport           = errors.New("github authorization transport failure")
	errRefreshRejected     = errors.New("github refresh was rejected")
	errRepositoryCheck     = errors.New("repository check failed")
)

// Config is the machine-local authorization configuration.  ClientID,
// RepositoryID, Owner, Repo, and Branch come from config.Config; the endpoint
// fields exist so tests can point at a fake GitHub.
type Config struct {
	ClientID     string
	RepositoryID int64
	Owner        string
	Repo         string
	Branch       string

	DeviceCodeURL string
	TokenURL      string
	APIBaseURL    string

	HTTPClient       *http.Client
	Now              func() time.Time
	Sleep            func(ctx context.Context, d time.Duration) error
	RefreshThreshold time.Duration
	Verifier         RepositoryVerifier
}

// Manager owns the stored credential, the resumable device transaction, and
// the auth state exposed through status messages.
type Manager struct {
	store            Store
	clientID         string
	repositoryID     int64
	owner            string
	repo             string
	branch           string
	deviceCodeURL    string
	tokenURL         string
	httpClient       *http.Client
	now              func() time.Time
	sleep            func(context.Context, time.Duration) error
	refreshThreshold time.Duration
	verifier         RepositoryVerifier

	flowMu sync.Mutex

	mu          sync.Mutex
	state       protocol.AuthState
	credential  *Credential
	transaction *DeviceTransaction
	challenge   *Challenge
}

// NewManager validates configuration and restores persisted auth state.  It
// performs no network access.
func NewManager(store Store, cfg Config) (*Manager, error) {
	if store == nil {
		return nil, errors.New("auth: credential store is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, errors.New("auth: client ID is required")
	}
	if cfg.RepositoryID <= 0 {
		return nil, errors.New("auth: repository ID is required")
	}
	if strings.TrimSpace(cfg.Owner) == "" || strings.TrimSpace(cfg.Repo) == "" || strings.TrimSpace(cfg.Branch) == "" {
		return nil, errors.New("auth: owner, repo, and branch are required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: authRequestTimeout}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	threshold := cfg.RefreshThreshold
	if threshold <= 0 {
		threshold = DefaultRefreshThreshold
	}
	manager := &Manager{
		store:            store,
		clientID:         cfg.ClientID,
		repositoryID:     cfg.RepositoryID,
		owner:            cfg.Owner,
		repo:             cfg.Repo,
		branch:           cfg.Branch,
		deviceCodeURL:    firstNonEmpty(cfg.DeviceCodeURL, DefaultDeviceCodeURL),
		tokenURL:         firstNonEmpty(cfg.TokenURL, DefaultTokenURL),
		httpClient:       httpClient,
		now:              now,
		sleep:            sleep,
		refreshThreshold: threshold,
		state:            protocol.AuthNotConnected,
	}
	manager.verifier = cfg.Verifier
	if manager.verifier == nil {
		manager.verifier = &HTTPRepositoryVerifier{
			BaseURL:      firstNonEmpty(cfg.APIBaseURL, DefaultAPIBaseURL),
			Owner:        cfg.Owner,
			Repo:         cfg.Repo,
			Branch:       cfg.Branch,
			RepositoryID: cfg.RepositoryID,
			HTTPClient:   httpClient,
		}
	}
	if err := manager.restore(); err != nil {
		return nil, err
	}
	return manager, nil
}

// restore loads persisted state without any network access.  An unexpired
// transaction resumes the same device code; an expired transaction is deleted
// so the next connect starts a new flow.
func (m *Manager) restore() error {
	credential, err := m.store.LoadCredential()
	switch {
	case err == nil:
		if credential == nil || credential.AccessToken == "" {
			return errors.New("auth: stored credential is incomplete")
		}
		m.credential = credential
		if credentialNearExpiry(credential, m.now(), 0) && credential.RefreshToken == "" {
			m.state = protocol.AuthReauthorizationRequired
		} else {
			m.state = protocol.AuthConnected
		}
	case errors.Is(err, ErrNoCredential):
	default:
		return errors.New("auth: stored credential is unreadable")
	}

	transaction, err := m.store.LoadTransaction()
	switch {
	case err == nil:
		if transaction != nil && m.now().Before(transaction.ExpiresAt) {
			if m.state == protocol.AuthConnected {
				// A completed flow must not leave a stale transaction behind.
				_ = m.store.DeleteTransaction()
			} else {
				m.transaction = transaction
				challenge := challengeFromTransaction(*transaction)
				m.challenge = &challenge
				m.state = protocol.AuthAuthorizing
			}
		} else {
			_ = m.store.DeleteTransaction()
		}
	case errors.Is(err, ErrNoTransaction):
	default:
		return errors.New("auth: stored device transaction is unreadable")
	}
	return nil
}

// State returns the current auth state for status messages.
func (m *Manager) State() protocol.AuthState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Challenge returns the active device-flow challenge, or nil when no
// authorization is in flight.
func (m *Manager) Challenge() *Challenge {
	m.mu.Lock()
	defer m.mu.Unlock()
	return copyChallenge(m.challenge)
}

// Begin validates an existing credential or starts/resumes a device flow.
// When it returns a nil challenge the manager is already connected; otherwise
// the returned challenge is also available from Challenge() while polling.
func (m *Manager) Begin(ctx context.Context) (*Challenge, error) {
	m.flowMu.Lock()
	defer m.flowMu.Unlock()
	return m.beginLocked(ctx)
}

func (m *Manager) beginLocked(ctx context.Context) (*Challenge, error) {
	if m.credentialCopy() != nil {
		_, err := m.authorizationHeaderLocked(ctx, false)
		switch {
		case err == nil:
			credential := m.credentialCopy()
			switch verifyErr := m.verifier.VerifyRepository(ctx, *credential); {
			case verifyErr == nil:
				m.setState(protocol.AuthConnected)
				m.setChallenge(nil)
				return nil, nil
			case errors.Is(verifyErr, ErrTargetRepositoryUnavailable):
				m.clearCredential()
				m.setState(protocol.AuthTargetRepositoryUnavailable)
				return nil, verifyErr
			case errors.Is(verifyErr, ErrReauthorizationRequired):
				m.clearCredential()
			default:
				m.setState(protocol.AuthNotConnected)
				return nil, verifyErr
			}
		case errors.Is(err, ErrNotConnected), errors.Is(err, ErrReauthorizationRequired):
			m.clearCredential()
		default:
			m.setState(protocol.AuthNotConnected)
			return nil, err
		}
	}

	if transaction := m.transactionCopy(); transaction != nil && m.now().Before(transaction.ExpiresAt) {
		challenge := challengeFromTransaction(*transaction)
		m.setChallenge(&challenge)
		m.setState(protocol.AuthAuthorizing)
		return &challenge, nil
	}
	if m.transactionCopy() != nil {
		if err := m.store.DeleteTransaction(); err != nil {
			return nil, err
		}
		m.setTransaction(nil)
	}
	return m.requestDeviceCode(ctx)
}

// Poll performs exactly one token poll for the active device transaction.  A
// pending or slow_down response keeps the authorizing state; a terminal
// response deletes the transaction and returns reauthorization_required.
func (m *Manager) Poll(ctx context.Context) (protocol.AuthState, error) {
	m.flowMu.Lock()
	defer m.flowMu.Unlock()
	return m.pollLocked(ctx)
}

func (m *Manager) pollLocked(ctx context.Context) (protocol.AuthState, error) {
	transaction := m.transactionCopy()
	if transaction == nil || m.State() != protocol.AuthAuthorizing {
		return m.State(), nil
	}
	if !m.now().Before(transaction.ExpiresAt) {
		m.discardTransaction()
		m.setChallenge(nil)
		m.setState(protocol.AuthReauthorizationRequired)
		return m.State(), ErrReauthorizationRequired
	}

	form := url.Values{}
	form.Set("client_id", m.clientID)
	form.Set("device_code", transaction.DeviceCode)
	form.Set("grant_type", deviceGrantType)
	form.Set("repository_id", strconv.FormatInt(m.repositoryID, 10))

	body, err := m.postForm(ctx, m.tokenURL, form)
	if err != nil {
		// Transport failures are transient: keep the persisted transaction
		// and keep polling until the server-provided deadline.
		return protocol.AuthAuthorizing, nil
	}
	var response tokenResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return protocol.AuthAuthorizing, nil
	}
	if response.Error != "" {
		switch response.Error {
		case "authorization_pending":
			return protocol.AuthAuthorizing, nil
		case "slow_down":
			interval := transaction.Interval + slowDownIncrement
			if response.Interval > 0 {
				interval = time.Duration(response.Interval) * time.Second
			}
			updated := *transaction
			updated.Interval = interval
			if err := m.store.SaveTransaction(updated); err != nil {
				return protocol.AuthAuthorizing, err
			}
			m.setTransaction(&updated)
			challenge := challengeFromTransaction(updated)
			m.setChallenge(&challenge)
			return protocol.AuthAuthorizing, nil
		default:
			return m.failDeviceFlow()
		}
	}
	if response.AccessToken == "" {
		return m.failDeviceFlow()
	}

	credential := Credential{
		AccessToken:        response.AccessToken,
		RefreshToken:       response.RefreshToken,
		TokenType:          response.TokenType,
		RepositoryID:       m.repositoryID,
		RepositoryFullName: m.owner + "/" + m.repo,
	}
	if response.ExpiresIn > 0 {
		credential.AccessTokenExpiresAt = m.now().Add(time.Duration(response.ExpiresIn) * time.Second)
	}
	if response.RefreshTokenExpiresIn > 0 {
		credential.RefreshTokenExpiresAt = m.now().Add(time.Duration(response.RefreshTokenExpiresIn) * time.Second)
	}
	if err := m.store.SaveCredential(credential); err != nil {
		m.setState(protocol.AuthNotConnected)
		return m.State(), err
	}
	m.setCredential(&credential)
	m.discardTransaction()
	m.setChallenge(nil)

	switch verifyErr := m.verifier.VerifyRepository(ctx, credential); {
	case verifyErr == nil:
		m.setState(protocol.AuthConnected)
		return m.State(), nil
	case errors.Is(verifyErr, ErrTargetRepositoryUnavailable):
		m.clearCredential()
		m.setState(protocol.AuthTargetRepositoryUnavailable)
		return m.State(), verifyErr
	case errors.Is(verifyErr, ErrReauthorizationRequired):
		m.clearCredential()
		m.setState(protocol.AuthReauthorizationRequired)
		return m.State(), verifyErr
	default:
		m.setState(protocol.AuthNotConnected)
		return m.State(), verifyErr
	}
}

// Connect drives Begin and Poll until the flow reaches a terminal state or
// the context is canceled.  Callers run it in the background and read
// State()/Challenge() for status snapshots.
func (m *Manager) Connect(ctx context.Context) (protocol.AuthState, error) {
	challenge, err := m.Begin(ctx)
	if err != nil {
		return m.State(), err
	}
	if challenge == nil {
		return m.State(), nil
	}
	for {
		state, err := m.Poll(ctx)
		if err != nil {
			return state, err
		}
		if state != protocol.AuthAuthorizing {
			return state, nil
		}
		if err := m.sleep(ctx, m.pollInterval()); err != nil {
			return m.State(), err
		}
	}
}

// AuthorizationHeader returns "Bearer <token>", refreshing first when the
// stored token is near expiry.  This is the only credential exposure allowed
// outside this package, and it stays in memory.
func (m *Manager) AuthorizationHeader(ctx context.Context) (string, error) {
	m.flowMu.Lock()
	defer m.flowMu.Unlock()
	return m.authorizationHeaderLocked(ctx, false)
}

// ForceRefresh refreshes exactly once after a 401.  It returns
// ErrReauthorizationRequired when no refresh is possible or the refresh token
// is rejected.
func (m *Manager) ForceRefresh(ctx context.Context) (string, error) {
	m.flowMu.Lock()
	defer m.flowMu.Unlock()
	return m.authorizationHeaderLocked(ctx, true)
}

func (m *Manager) authorizationHeaderLocked(ctx context.Context, force bool) (string, error) {
	credential := m.credentialCopy()
	if credential == nil || credential.AccessToken == "" {
		m.setState(protocol.AuthNotConnected)
		return "", ErrNotConnected
	}
	now := m.now()
	expiring := !credential.AccessTokenExpiresAt.IsZero() && !now.Before(credential.AccessTokenExpiresAt.Add(-m.refreshThreshold))
	noUsableExpiry := credential.AccessTokenExpiresAt.IsZero()

	if force || ((expiring || noUsableExpiry) && credential.RefreshToken != "") {
		if credential.RefreshToken == "" {
			m.setState(protocol.AuthReauthorizationRequired)
			return "", ErrReauthorizationRequired
		}
		updated, err := m.refresh(ctx, credential)
		if err != nil {
			if errors.Is(err, errRefreshRejected) {
				m.setState(protocol.AuthReauthorizationRequired)
				return "", ErrReauthorizationRequired
			}
			return "", err
		}
		m.setCredential(updated)
		m.setState(protocol.AuthConnected)
		credential = updated
	} else if expiring && credential.RefreshToken == "" {
		m.setState(protocol.AuthReauthorizationRequired)
		return "", ErrReauthorizationRequired
	}
	return "Bearer " + credential.AccessToken, nil
}

// refresh replaces the whole credential record atomically.  A failed save
// leaves the previous in-memory credential untouched.
func (m *Manager) refresh(ctx context.Context, credential *Credential) (*Credential, error) {
	form := url.Values{}
	form.Set("client_id", m.clientID)
	form.Set("grant_type", refreshGrantType)
	form.Set("refresh_token", credential.RefreshToken)

	body, err := m.postForm(ctx, m.tokenURL, form)
	if err != nil {
		return nil, err
	}
	var response tokenResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errInvalidAuthResponse
	}
	if response.Error != "" || response.AccessToken == "" {
		return nil, errRefreshRejected
	}
	updated := *credential
	updated.AccessToken = response.AccessToken
	if response.TokenType != "" {
		updated.TokenType = response.TokenType
	}
	if response.ExpiresIn > 0 {
		updated.AccessTokenExpiresAt = m.now().Add(time.Duration(response.ExpiresIn) * time.Second)
	} else {
		updated.AccessTokenExpiresAt = time.Time{}
	}
	if response.RefreshToken != "" {
		updated.RefreshToken = response.RefreshToken
	}
	if response.RefreshTokenExpiresIn > 0 {
		updated.RefreshTokenExpiresAt = m.now().Add(time.Duration(response.RefreshTokenExpiresIn) * time.Second)
	}
	if err := m.store.SaveCredential(updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

// Reset deletes both Keychain records and clears the in-memory state.  Queue
// jobs are untouched.
func (m *Manager) Reset() error {
	m.flowMu.Lock()
	defer m.flowMu.Unlock()
	var failures []error
	if err := m.store.DeleteCredential(); err != nil {
		failures = append(failures, err)
	}
	if err := m.store.DeleteTransaction(); err != nil {
		failures = append(failures, err)
	}
	m.setCredential(nil)
	m.setTransaction(nil)
	m.setChallenge(nil)
	m.setState(protocol.AuthNotConnected)
	return errors.Join(failures...)
}

func (m *Manager) requestDeviceCode(ctx context.Context) (*Challenge, error) {
	form := url.Values{}
	form.Set("client_id", m.clientID)

	body, err := m.postForm(ctx, m.deviceCodeURL, form)
	if err != nil {
		m.setState(protocol.AuthNotConnected)
		return nil, err
	}
	var response deviceCodeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		m.setState(protocol.AuthNotConnected)
		return nil, errInvalidAuthResponse
	}
	if response.Error != "" {
		m.setState(protocol.AuthReauthorizationRequired)
		return nil, ErrReauthorizationRequired
	}
	if response.DeviceCode == "" || response.UserCode == "" || response.VerificationURI == "" || response.ExpiresIn <= 0 {
		m.setState(protocol.AuthNotConnected)
		return nil, errInvalidAuthResponse
	}
	interval := time.Duration(response.Interval) * time.Second
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	transaction := DeviceTransaction{
		DeviceCode:              response.DeviceCode,
		UserCode:                response.UserCode,
		VerificationURI:         response.VerificationURI,
		VerificationURIComplete: completeVerificationURI(response.VerificationURI, response.UserCode, response.VerificationURIComplete),
		ExpiresAt:               m.now().Add(time.Duration(response.ExpiresIn) * time.Second),
		Interval:                interval,
	}
	if err := m.store.SaveTransaction(transaction); err != nil {
		m.setState(protocol.AuthNotConnected)
		return nil, err
	}
	m.setTransaction(&transaction)
	challenge := challengeFromTransaction(transaction)
	m.setChallenge(&challenge)
	m.setState(protocol.AuthAuthorizing)
	return &challenge, nil
}

// completeVerificationURI returns the server-provided
// verification_uri_complete when present. GitHub currently omits it from the
// device-code response, so a valid github.com verification URI is prefilled
// with the user code; the popup can then authorize with one click.
func completeVerificationURI(verificationURI, userCode, complete string) string {
	if complete != "" {
		return complete
	}
	if verificationURI == "" || userCode == "" {
		return ""
	}
	parsed, err := url.Parse(verificationURI)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return ""
	}
	query := parsed.Query()
	query.Set("user_code", userCode)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (m *Manager) failDeviceFlow() (protocol.AuthState, error) {
	m.discardTransaction()
	m.setChallenge(nil)
	m.setState(protocol.AuthReauthorizationRequired)
	return m.State(), ErrReauthorizationRequired
}

func (m *Manager) postForm(ctx context.Context, endpoint string, form url.Values) ([]byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, authRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errTransport
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "lecture-transcripts-uploader")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return nil, errTransport
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAuthResponseBytes+1))
	if err != nil || int64(len(body)) > maxAuthResponseBytes {
		return nil, errTransport
	}
	if response.StatusCode >= 500 {
		return nil, errTransport
	}
	return body, nil
}

func (m *Manager) pollInterval() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.challenge != nil && m.challenge.Interval > 0 {
		return m.challenge.Interval
	}
	return DefaultPollInterval
}

func (m *Manager) credentialCopy() *Credential {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.credential == nil {
		return nil
	}
	credential := *m.credential
	return &credential
}

func (m *Manager) setCredential(credential *Credential) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.credential = credential
}

func (m *Manager) transactionCopy() *DeviceTransaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.transaction == nil {
		return nil
	}
	transaction := *m.transaction
	return &transaction
}

func (m *Manager) setTransaction(transaction *DeviceTransaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transaction = transaction
}

func (m *Manager) setChallenge(challenge *Challenge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.challenge = copyChallenge(challenge)
}

func (m *Manager) setState(state protocol.AuthState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
}

func (m *Manager) clearCredential() {
	if m.credentialCopy() != nil {
		_ = m.store.DeleteCredential()
	}
	m.setCredential(nil)
}

func (m *Manager) discardTransaction() {
	if m.transactionCopy() != nil {
		_ = m.store.DeleteTransaction()
	}
	m.setTransaction(nil)
}

func copyChallenge(challenge *Challenge) *Challenge {
	if challenge == nil {
		return nil
	}
	copied := *challenge
	return &copied
}

func challengeFromTransaction(transaction DeviceTransaction) Challenge {
	return Challenge{
		UserCode:                transaction.UserCode,
		VerificationURI:         transaction.VerificationURI,
		VerificationURIComplete: transaction.VerificationURIComplete,
		ExpiresAt:               transaction.ExpiresAt,
		Interval:                transaction.Interval,
	}
}

func credentialNearExpiry(credential *Credential, now time.Time, threshold time.Duration) bool {
	if credential == nil || credential.AccessTokenExpiresAt.IsZero() {
		return false
	}
	return !now.Before(credential.AccessTokenExpiresAt.Add(-threshold))
}

func defaultSleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	Error                   string `json:"error"`
}

type tokenResponse struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	Interval              int    `json:"interval"`
	Error                 string `json:"error"`
}

// HTTPRepositoryVerifier performs the two connect sanity requests:
// GET /repos/{owner}/{repo} verifies the numeric repository ID and full name,
// and GET /repos/{owner}/{repo}/contents?ref={branch} proves the branch is
// initialized and the Contents permission is usable.  A 403 or 404 on either
// request is target_repository_unavailable; a 401 is reauthorization_required.
type HTTPRepositoryVerifier struct {
	BaseURL      string
	Owner        string
	Repo         string
	Branch       string
	RepositoryID int64
	HTTPClient   *http.Client
}

// VerifyRepository implements RepositoryVerifier.
func (v *HTTPRepositoryVerifier) VerifyRepository(ctx context.Context, credential Credential) error {
	if strings.TrimSpace(credential.AccessToken) == "" {
		return ErrReauthorizationRequired
	}
	client := v.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: authRequestTimeout}
	}
	base := strings.TrimRight(firstNonEmpty(v.BaseURL, DefaultAPIBaseURL), "/")
	repoEndpoint := base + "/repos/" + url.PathEscape(v.Owner) + "/" + url.PathEscape(v.Repo)

	body, status, err := v.get(ctx, client, repoEndpoint, credential)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		var repository struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
		}
		if err := json.Unmarshal(body, &repository); err != nil {
			return errRepositoryCheck
		}
		if repository.ID != v.RepositoryID || !strings.EqualFold(repository.FullName, v.Owner+"/"+v.Repo) {
			return ErrTargetRepositoryUnavailable
		}
	case http.StatusUnauthorized:
		return ErrReauthorizationRequired
	case http.StatusForbidden, http.StatusNotFound:
		return ErrTargetRepositoryUnavailable
	default:
		return errRepositoryCheck
	}

	contentsEndpoint := repoEndpoint + "/contents?ref=" + url.QueryEscape(v.Branch)
	_, status, err = v.get(ctx, client, contentsEndpoint, credential)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return ErrReauthorizationRequired
	case http.StatusForbidden, http.StatusNotFound:
		return ErrTargetRepositoryUnavailable
	default:
		return errRepositoryCheck
	}
}

func (v *HTTPRepositoryVerifier) get(ctx context.Context, client *http.Client, endpoint string, credential Credential) ([]byte, int, error) {
	requestCtx, cancel := context.WithTimeout(ctx, authRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, errRepositoryCheck
	}
	request.Header.Set("Accept", githubAccept)
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "lecture-transcripts-uploader")
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, errRepositoryCheck
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAuthResponseBytes+1))
	if err != nil || int64(len(body)) > maxAuthResponseBytes {
		return nil, 0, errRepositoryCheck
	}
	return body, response.StatusCode, nil
}
