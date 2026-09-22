package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

const (
	testClientID     = "Iv1.testclient"
	testRepositoryID = int64(424242)
)

var testNow = time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: testNow}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeStore struct {
	credential         *Credential
	transaction        *DeviceTransaction
	credentialSaves    int
	transactionSaves   int
	credentialDeletes  int
	transactionDeletes int
	saveCredentialErr  error
}

func (s *fakeStore) LoadCredential() (*Credential, error) {
	if s.credential == nil {
		return nil, ErrNoCredential
	}
	copied := *s.credential
	return &copied, nil
}

func (s *fakeStore) SaveCredential(credential Credential) error {
	if s.saveCredentialErr != nil {
		return s.saveCredentialErr
	}
	s.credentialSaves++
	copied := credential
	s.credential = &copied
	return nil
}

func (s *fakeStore) DeleteCredential() error {
	s.credentialDeletes++
	s.credential = nil
	return nil
}

func (s *fakeStore) LoadTransaction() (*DeviceTransaction, error) {
	if s.transaction == nil {
		return nil, ErrNoTransaction
	}
	copied := *s.transaction
	return &copied, nil
}

func (s *fakeStore) SaveTransaction(transaction DeviceTransaction) error {
	s.transactionSaves++
	copied := transaction
	s.transaction = &copied
	return nil
}

func (s *fakeStore) DeleteTransaction() error {
	s.transactionDeletes++
	s.transaction = nil
	return nil
}

type fakeVerifier struct {
	err        error
	calls      int
	credential Credential
}

func (v *fakeVerifier) VerifyRepository(_ context.Context, credential Credential) error {
	v.calls++
	v.credential = credential
	return v.err
}

type authServer struct {
	*httptest.Server
	mu              sync.Mutex
	deviceCodeCalls int
	tokenCalls      int
	tokenForms      []url.Values
	repoCalls       int
	contentsCalls   int

	deviceCodeStatus int
	deviceCodeBody   string
	tokenStatuses    []int
	tokenBodies      []string
	repoStatus       int
	repoBody         string
	contentsStatus   int
	contentsBody     string
}

const defaultDeviceCodeBody = `{"device_code":"device-secret","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","verification_uri_complete":"https://github.com/login/device?user_code=ABCD-1234","expires_in":900,"interval":5}`

const defaultTokenBody = `{"access_token":"ghu_token","token_type":"bearer","expires_in":28800,"refresh_token":"ghr_refresh","refresh_token_expires_in":15811200}`

const defaultRepoBody = `{"id":424242,"full_name":"neelbangera/lecture-transcripts","default_branch":"main"}`

func newAuthServer(t *testing.T) *authServer {
	t.Helper()
	server := &authServer{
		deviceCodeStatus: http.StatusOK,
		deviceCodeBody:   defaultDeviceCodeBody,
		tokenBodies:      []string{defaultTokenBody},
		repoStatus:       http.StatusOK,
		repoBody:         defaultRepoBody,
		contentsStatus:   http.StatusOK,
		contentsBody:     `[]`,
	}
	server.Server = httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(server.Close)
	return server
}

func (s *authServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/login/device/code":
		s.deviceCodeCalls++
		w.WriteHeader(s.deviceCodeStatus)
		io.WriteString(w, s.deviceCodeBody)
	case "/login/oauth/access_token":
		s.tokenCalls++
		_ = r.ParseForm()
		copied := url.Values{}
		for key, values := range r.PostForm {
			copied[key] = append([]string(nil), values...)
		}
		s.tokenForms = append(s.tokenForms, copied)
		index := s.tokenCalls - 1
		status := http.StatusOK
		if index < len(s.tokenStatuses) {
			status = s.tokenStatuses[index]
		}
		w.WriteHeader(status)
		body := s.tokenBodies[len(s.tokenBodies)-1]
		if index < len(s.tokenBodies) {
			body = s.tokenBodies[index]
		}
		io.WriteString(w, body)
	case "/repos/neelbangera/lecture-transcripts":
		s.repoCalls++
		w.WriteHeader(s.repoStatus)
		io.WriteString(w, s.repoBody)
	case "/repos/neelbangera/lecture-transcripts/contents":
		s.contentsCalls++
		w.WriteHeader(s.contentsStatus)
		io.WriteString(w, s.contentsBody)
	default:
		http.NotFound(w, r)
	}
}

func (s *authServer) tokenForm(index int) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index >= len(s.tokenForms) {
		return nil
	}
	return s.tokenForms[index]
}

func (s *authServer) calls() (deviceCode, token, repo, contents int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceCodeCalls, s.tokenCalls, s.repoCalls, s.contentsCalls
}

func testManager(t *testing.T, store Store, server *authServer, verifier RepositoryVerifier, clock *fakeClock) *Manager {
	t.Helper()
	manager, err := NewManager(store, Config{
		ClientID:      testClientID,
		RepositoryID:  testRepositoryID,
		Owner:         "neelbangera",
		Repo:          "lecture-transcripts",
		Branch:        "main",
		DeviceCodeURL: server.URL + "/login/device/code",
		TokenURL:      server.URL + "/login/oauth/access_token",
		APIBaseURL:    server.URL,
		Now:           clock.Now,
		Sleep:         func(context.Context, time.Duration) error { return nil },
		Verifier:      verifier,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

func TestBeginPersistsTransaction(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	clock := newFakeClock()
	verifier := &fakeVerifier{}
	manager := testManager(t, store, server, verifier, clock)

	challenge, err := manager.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if challenge == nil {
		t.Fatal("expected a challenge for a new flow")
	}
	if challenge.UserCode != "ABCD-1234" {
		t.Fatalf("user code = %q", challenge.UserCode)
	}
	if challenge.VerificationURI != "https://github.com/login/device" {
		t.Fatalf("verification URI = %q", challenge.VerificationURI)
	}
	if challenge.VerificationURIComplete == "" {
		t.Fatal("expected verification_uri_complete to be preserved")
	}
	if want := testNow.Add(900 * time.Second); !challenge.ExpiresAt.Equal(want) {
		t.Fatalf("expires at = %s, want %s", challenge.ExpiresAt, want)
	}
	if challenge.Interval != 5*time.Second {
		t.Fatalf("interval = %s, want 5s", challenge.Interval)
	}
	if store.transaction == nil {
		t.Fatal("transaction must be persisted before polling")
	}
	if store.transaction.DeviceCode != "device-secret" {
		t.Fatalf("stored device code = %q", store.transaction.DeviceCode)
	}
	if manager.State() != protocol.AuthAuthorizing {
		t.Fatalf("state = %s, want authorizing", manager.State())
	}
	deviceCodeCalls, tokenCalls, _, _ := server.calls()
	if deviceCodeCalls != 1 || tokenCalls != 0 {
		t.Fatalf("device code calls = %d, token calls = %d; want 1 and 0", deviceCodeCalls, tokenCalls)
	}
}

func TestBeginSynthesizesCompleteVerificationURI(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	server.deviceCodeBody = `{"device_code":"device-secret","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`
	clock := newFakeClock()
	verifier := &fakeVerifier{}
	manager := testManager(t, store, server, verifier, clock)

	challenge, err := manager.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if challenge == nil {
		t.Fatal("expected a challenge for a new flow")
	}
	if want := "https://github.com/login/device?user_code=ABCD-1234"; challenge.VerificationURIComplete != want {
		t.Fatalf("verification URI complete = %q, want %q", challenge.VerificationURIComplete, want)
	}
	if store.transaction == nil || store.transaction.VerificationURIComplete != challenge.VerificationURIComplete {
		t.Fatal("synthesized complete URI must be persisted with the transaction")
	}
}

func TestPollPendingSlowDownThenSuccess(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	server.tokenBodies = []string{
		`{"error":"authorization_pending"}`,
		`{"error":"slow_down","interval":11}`,
		defaultTokenBody,
	}
	clock := newFakeClock()
	verifier := &fakeVerifier{}
	manager := testManager(t, store, server, verifier, clock)

	if _, err := manager.Begin(context.Background()); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	state, err := manager.Poll(context.Background())
	if err != nil || state != protocol.AuthAuthorizing {
		t.Fatalf("pending poll = (%s, %v), want authorizing", state, err)
	}
	state, err = manager.Poll(context.Background())
	if err != nil || state != protocol.AuthAuthorizing {
		t.Fatalf("slow_down poll = (%s, %v), want authorizing", state, err)
	}
	if manager.Challenge() == nil || manager.Challenge().Interval != 11*time.Second {
		t.Fatalf("slow_down interval = %v, want 11s", manager.Challenge())
	}
	if store.transaction == nil || store.transaction.Interval != 11*time.Second {
		t.Fatalf("persisted interval = %v, want 11s", store.transaction)
	}
	state, err = manager.Poll(context.Background())
	if err != nil {
		t.Fatalf("success poll: %v", err)
	}
	if state != protocol.AuthConnected {
		t.Fatalf("state = %s, want connected", state)
	}
	if store.credential == nil || store.credential.AccessToken != "ghu_token" {
		t.Fatalf("credential = %+v", store.credential)
	}
	if store.credential.RepositoryID != testRepositoryID || store.credential.RepositoryFullName != "neelbangera/lecture-transcripts" {
		t.Fatalf("credential repository identity = %+v", store.credential)
	}
	if store.transaction != nil {
		t.Fatal("transaction must be deleted on success")
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	if verifier.credential.AccessToken != "ghu_token" {
		t.Fatalf("verifier credential token = %q", verifier.credential.AccessToken)
	}
	form := server.tokenForm(0)
	if form.Get("client_id") != testClientID {
		t.Fatalf("client_id = %q", form.Get("client_id"))
	}
	if form.Get("device_code") != "device-secret" {
		t.Fatalf("device_code = %q", form.Get("device_code"))
	}
	if form.Get("grant_type") != deviceGrantType {
		t.Fatalf("grant_type = %q", form.Get("grant_type"))
	}
	if form.Get("repository_id") != "424242" {
		t.Fatalf("repository_id = %q, want configured numeric repository restriction", form.Get("repository_id"))
	}
}

func TestPollTerminalErrorDeletesTransaction(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	server.tokenBodies = []string{`{"error":"access_denied"}`}
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if _, err := manager.Begin(context.Background()); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	state, err := manager.Poll(context.Background())
	if !errors.Is(err, ErrReauthorizationRequired) {
		t.Fatalf("poll error = %v, want ErrReauthorizationRequired", err)
	}
	if state != protocol.AuthReauthorizationRequired {
		t.Fatalf("state = %s, want reauthorization_required", state)
	}
	if store.transaction != nil {
		t.Fatal("terminal error must delete the transaction")
	}
	if store.credential != nil {
		t.Fatal("terminal error must not store a credential")
	}
	if strings.Contains(err.Error(), "device-secret") || strings.Contains(err.Error(), "ghu_") {
		t.Fatalf("error leaked secret material: %v", err)
	}
}

func TestPollInstallationMissingMapsToTargetUnavailable(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	server.tokenBodies = []string{`{"error":"installation_missing_access"}`}
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if _, err := manager.Begin(context.Background()); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	state, err := manager.Poll(context.Background())
	if !errors.Is(err, ErrTargetRepositoryUnavailable) {
		t.Fatalf("poll error = %v, want ErrTargetRepositoryUnavailable", err)
	}
	if state != protocol.AuthTargetRepositoryUnavailable {
		t.Fatalf("state = %s, want target_repository_unavailable", state)
	}
	if store.transaction != nil {
		t.Fatal("installation failure must discard the transaction")
	}
	if store.credential != nil {
		t.Fatal("installation failure must not store a credential")
	}
}

func TestPollStopsAtServerExpiry(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if _, err := manager.Begin(context.Background()); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	clock.Advance(901 * time.Second)
	state, err := manager.Poll(context.Background())
	if !errors.Is(err, ErrReauthorizationRequired) {
		t.Fatalf("poll error = %v, want ErrReauthorizationRequired", err)
	}
	if state != protocol.AuthReauthorizationRequired {
		t.Fatalf("state = %s", state)
	}
	if store.transaction != nil {
		t.Fatal("expired transaction must be deleted")
	}
	_, tokenCalls, _, _ := server.calls()
	if tokenCalls != 0 {
		t.Fatalf("token calls = %d, want 0 after expiry", tokenCalls)
	}
}

func TestBeginResumesPersistedTransaction(t *testing.T) {
	store := &fakeStore{transaction: &DeviceTransaction{
		DeviceCode:      "persisted-device",
		UserCode:        "WXYZ-9876",
		VerificationURI: "https://github.com/login/device",
		ExpiresAt:       testNow.Add(10 * time.Minute),
		Interval:        7 * time.Second,
	}}
	server := newAuthServer(t)
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if manager.State() != protocol.AuthAuthorizing {
		t.Fatalf("restored state = %s, want authorizing", manager.State())
	}
	challenge, err := manager.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if challenge == nil || challenge.UserCode != "WXYZ-9876" {
		t.Fatalf("challenge = %+v, want resumed transaction", challenge)
	}
	if challenge.Interval != 7*time.Second {
		t.Fatalf("interval = %s, want persisted 7s", challenge.Interval)
	}
	deviceCodeCalls, _, _, _ := server.calls()
	if deviceCodeCalls != 0 {
		t.Fatalf("device code calls = %d, want 0 when resuming", deviceCodeCalls)
	}
}

func TestBeginReplacesExpiredTransaction(t *testing.T) {
	store := &fakeStore{transaction: &DeviceTransaction{
		DeviceCode: "expired-device",
		UserCode:   "OLD-0000",
		ExpiresAt:  testNow.Add(-time.Minute),
		Interval:   5 * time.Second,
	}}
	server := newAuthServer(t)
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if manager.State() != protocol.AuthNotConnected {
		t.Fatalf("restored state = %s, want not_connected", manager.State())
	}
	if store.transactionDeletes == 0 {
		t.Fatal("expired transaction must be deleted during restore")
	}
	challenge, err := manager.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if challenge == nil || challenge.UserCode != "ABCD-1234" {
		t.Fatalf("challenge = %+v, want a fresh flow", challenge)
	}
	deviceCodeCalls, _, _, _ := server.calls()
	if deviceCodeCalls != 1 {
		t.Fatalf("device code calls = %d, want 1", deviceCodeCalls)
	}
}

func TestConnectRunsToCompletion(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	clock := newFakeClock()
	verifier := &fakeVerifier{}
	manager := testManager(t, store, server, verifier, clock)

	state, err := manager.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if state != protocol.AuthConnected {
		t.Fatalf("state = %s, want connected", state)
	}
	if store.credential == nil {
		t.Fatal("Connect must store the credential")
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
}

func TestSanityFailureClearsJustAuthorizedCredential(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want protocol.AuthState
	}{
		{name: "target unavailable", err: ErrTargetRepositoryUnavailable, want: protocol.AuthTargetRepositoryUnavailable},
		{name: "reauthorization required", err: ErrReauthorizationRequired, want: protocol.AuthReauthorizationRequired},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &fakeStore{}
			server := newAuthServer(t)
			clock := newFakeClock()
			manager := testManager(t, store, server, &fakeVerifier{err: testCase.err}, clock)

			if _, err := manager.Begin(context.Background()); err != nil {
				t.Fatalf("Begin: %v", err)
			}
			state, err := manager.Poll(context.Background())
			if !errors.Is(err, testCase.err) {
				t.Fatalf("poll error = %v, want %v", err, testCase.err)
			}
			if state != testCase.want {
				t.Fatalf("state = %s, want %s", state, testCase.want)
			}
			if store.credential != nil {
				t.Fatal("sanity failure must clear the just-authorized credential")
			}
			if store.credentialDeletes == 0 {
				t.Fatal("expected a credential delete")
			}
		})
	}
}

func TestHTTPRepositoryVerifier(t *testing.T) {
	cases := []struct {
		name           string
		repoStatus     int
		repoBody       string
		contentsStatus int
		want           error
	}{
		{name: "ok", repoStatus: http.StatusOK, repoBody: defaultRepoBody, contentsStatus: http.StatusOK},
		{name: "ok case-insensitive full name", repoStatus: http.StatusOK, repoBody: `{"id":424242,"full_name":"NeelBangera/Lecture-Transcripts"}`, contentsStatus: http.StatusOK},
		{name: "wrong numeric id", repoStatus: http.StatusOK, repoBody: `{"id":1,"full_name":"neelbangera/lecture-transcripts"}`, contentsStatus: http.StatusOK, want: ErrTargetRepositoryUnavailable},
		{name: "wrong full name", repoStatus: http.StatusOK, repoBody: `{"id":424242,"full_name":"someone/other"}`, contentsStatus: http.StatusOK, want: ErrTargetRepositoryUnavailable},
		{name: "repo 403", repoStatus: http.StatusForbidden, want: ErrTargetRepositoryUnavailable},
		{name: "repo 404", repoStatus: http.StatusNotFound, want: ErrTargetRepositoryUnavailable},
		{name: "repo 401", repoStatus: http.StatusUnauthorized, want: ErrReauthorizationRequired},
		{name: "contents 403", repoStatus: http.StatusOK, repoBody: defaultRepoBody, contentsStatus: http.StatusForbidden, want: ErrTargetRepositoryUnavailable},
		{name: "contents 404", repoStatus: http.StatusOK, repoBody: defaultRepoBody, contentsStatus: http.StatusNotFound, want: ErrTargetRepositoryUnavailable},
		{name: "contents 401", repoStatus: http.StatusOK, repoBody: defaultRepoBody, contentsStatus: http.StatusUnauthorized, want: ErrReauthorizationRequired},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newAuthServer(t)
			server.repoStatus = testCase.repoStatus
			if testCase.repoBody != "" {
				server.repoBody = testCase.repoBody
			}
			server.contentsStatus = testCase.contentsStatus

			verifier := &HTTPRepositoryVerifier{
				BaseURL:      server.URL,
				Owner:        "neelbangera",
				Repo:         "lecture-transcripts",
				Branch:       "main",
				RepositoryID: testRepositoryID,
				HTTPClient:   server.Client(),
			}
			err := verifier.VerifyRepository(context.Background(), Credential{AccessToken: "tok"})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("VerifyRepository = %v, want %v", err, testCase.want)
			}
			if testCase.want == nil {
				_, _, _, contentsCalls := server.calls()
				if contentsCalls != 1 {
					t.Fatalf("contents calls = %d, want 1", contentsCalls)
				}
			}
		})
	}
}

func TestHTTPRepositoryVerifierUsesBranchQueryAndHeaders(t *testing.T) {
	var captured *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/contents") {
			captured = r.Clone(context.Background())
		}
		if strings.Contains(r.URL.Path, "/contents") {
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "[]")
			return
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, defaultRepoBody)
	}))
	defer server.Close()

	verifier := &HTTPRepositoryVerifier{
		BaseURL:      server.URL,
		Owner:        "neelbangera",
		Repo:         "lecture-transcripts",
		Branch:       "main",
		RepositoryID: testRepositoryID,
		HTTPClient:   server.Client(),
	}
	if err := verifier.VerifyRepository(context.Background(), Credential{AccessToken: "tok"}); err != nil {
		t.Fatalf("VerifyRepository: %v", err)
	}
	if captured == nil {
		t.Fatal("contents request not observed")
	}
	if got := captured.URL.Query().Get("ref"); got != "main" {
		t.Fatalf("ref = %q, want main", got)
	}
	if got := captured.Header.Get("X-GitHub-Api-Version"); got != "2026-03-10" {
		t.Fatalf("X-GitHub-Api-Version = %q", got)
	}
	if got := captured.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestAuthorizationHeaderRefreshesNearExpiry(t *testing.T) {
	store := &fakeStore{credential: &Credential{
		AccessToken:          "old-token",
		RefreshToken:         "old-refresh",
		AccessTokenExpiresAt: testNow.Add(4 * time.Minute),
		RepositoryID:         testRepositoryID,
		RepositoryFullName:   "neelbangera/lecture-transcripts",
	}}
	server := newAuthServer(t)
	server.tokenBodies = []string{`{"access_token":"new-token","token_type":"bearer","expires_in":28800,"refresh_token":"new-refresh","refresh_token_expires_in":15811200}`}
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	header, err := manager.AuthorizationHeader(context.Background())
	if err != nil {
		t.Fatalf("AuthorizationHeader: %v", err)
	}
	if header != "Bearer new-token" {
		t.Fatalf("header = %q", header)
	}
	if store.credential == nil || store.credential.AccessToken != "new-token" || store.credential.RefreshToken != "new-refresh" {
		t.Fatalf("credential = %+v, want atomically replaced token pair", store.credential)
	}
	if store.credentialSaves != 1 {
		t.Fatalf("credential saves = %d, want exactly 1 atomic replace", store.credentialSaves)
	}
	form := server.tokenForm(0)
	if form.Get("grant_type") != refreshGrantType || form.Get("refresh_token") != "old-refresh" || form.Get("client_id") != testClientID {
		t.Fatalf("refresh form = %v", form)
	}
	if manager.State() != protocol.AuthConnected {
		t.Fatalf("state = %s, want connected", manager.State())
	}
}

func TestAuthorizationHeaderUsesValidTokenWithoutRefresh(t *testing.T) {
	store := &fakeStore{credential: &Credential{
		AccessToken:          "valid-token",
		RefreshToken:         "refresh",
		AccessTokenExpiresAt: testNow.Add(30 * time.Minute),
	}}
	server := newAuthServer(t)
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	header, err := manager.AuthorizationHeader(context.Background())
	if err != nil {
		t.Fatalf("AuthorizationHeader: %v", err)
	}
	if header != "Bearer valid-token" {
		t.Fatalf("header = %q", header)
	}
	_, tokenCalls, _, _ := server.calls()
	if tokenCalls != 0 {
		t.Fatalf("token calls = %d, want 0", tokenCalls)
	}
}

func TestAuthorizationHeaderExpiringWithoutRefreshToken(t *testing.T) {
	store := &fakeStore{credential: &Credential{
		AccessToken:          "expiring-token",
		AccessTokenExpiresAt: testNow.Add(4 * time.Minute),
	}}
	server := newAuthServer(t)
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	_, err := manager.AuthorizationHeader(context.Background())
	if !errors.Is(err, ErrReauthorizationRequired) {
		t.Fatalf("error = %v, want ErrReauthorizationRequired", err)
	}
	if manager.State() != protocol.AuthReauthorizationRequired {
		t.Fatalf("state = %s, want reauthorization_required", manager.State())
	}
	_, tokenCalls, _, _ := server.calls()
	if tokenCalls != 0 {
		t.Fatalf("token calls = %d, want 0", tokenCalls)
	}
}

func TestAuthorizationHeaderNotConnected(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	_, err := manager.AuthorizationHeader(context.Background())
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("error = %v, want ErrNotConnected", err)
	}
	if manager.State() != protocol.AuthNotConnected {
		t.Fatalf("state = %s, want not_connected", manager.State())
	}
}

func TestForceRefreshRejectsInvalidGrant(t *testing.T) {
	store := &fakeStore{credential: &Credential{
		AccessToken:          "old-token",
		RefreshToken:         "dead-refresh",
		AccessTokenExpiresAt: testNow.Add(30 * time.Minute),
	}}
	server := newAuthServer(t)
	server.tokenBodies = []string{`{"error":"invalid_grant"}`}
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	_, err := manager.ForceRefresh(context.Background())
	if !errors.Is(err, ErrReauthorizationRequired) {
		t.Fatalf("error = %v, want ErrReauthorizationRequired", err)
	}
	if manager.State() != protocol.AuthReauthorizationRequired {
		t.Fatalf("state = %s, want reauthorization_required", manager.State())
	}
}

func TestRefreshFailureLeavesCredentialUntouched(t *testing.T) {
	store := &fakeStore{
		credential: &Credential{
			AccessToken:          "old-token",
			RefreshToken:         "refresh",
			AccessTokenExpiresAt: testNow.Add(4 * time.Minute),
		},
		saveCredentialErr: errors.New("keychain write failed"),
	}
	server := newAuthServer(t)
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if _, err := manager.AuthorizationHeader(context.Background()); err == nil {
		t.Fatal("expected refresh save failure to surface")
	}
	if store.credential.AccessToken != "old-token" || store.credential.RefreshToken != "refresh" {
		t.Fatalf("credential changed after failed save: %+v", store.credential)
	}
	if manager.credentialCopy().AccessToken != "old-token" {
		t.Fatal("in-memory credential changed after failed save")
	}
}

func TestResetDeletesBothRecords(t *testing.T) {
	store := &fakeStore{
		credential:  &Credential{AccessToken: "tok"},
		transaction: &DeviceTransaction{DeviceCode: "device", UserCode: "CODE", ExpiresAt: testNow.Add(time.Hour)},
	}
	server := newAuthServer(t)
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if err := manager.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if store.credential != nil || store.transaction != nil {
		t.Fatalf("records remain after reset: %+v %+v", store.credential, store.transaction)
	}
	if store.credentialDeletes == 0 || store.transactionDeletes == 0 {
		t.Fatalf("deletes = %d/%d, want both", store.credentialDeletes, store.transactionDeletes)
	}
	if manager.State() != protocol.AuthNotConnected {
		t.Fatalf("state = %s, want not_connected", manager.State())
	}
}

func TestRestoreStateFromStore(t *testing.T) {
	cases := []struct {
		name        string
		credential  *Credential
		transaction *DeviceTransaction
		want        protocol.AuthState
	}{
		{
			name:       "valid credential",
			credential: &Credential{AccessToken: "tok", AccessTokenExpiresAt: testNow.Add(time.Hour)},
			want:       protocol.AuthConnected,
		},
		{
			name:       "expired without refresh",
			credential: &Credential{AccessToken: "tok", AccessTokenExpiresAt: testNow.Add(-time.Minute)},
			want:       protocol.AuthReauthorizationRequired,
		},
		{
			name:       "expired with refresh",
			credential: &Credential{AccessToken: "tok", RefreshToken: "refresh", AccessTokenExpiresAt: testNow.Add(-time.Minute)},
			want:       protocol.AuthConnected,
		},
		{
			name:        "resumable transaction",
			transaction: &DeviceTransaction{DeviceCode: "device", UserCode: "CODE", ExpiresAt: testNow.Add(time.Hour)},
			want:        protocol.AuthAuthorizing,
		},
		{
			name:        "expired transaction",
			transaction: &DeviceTransaction{DeviceCode: "device", UserCode: "CODE", ExpiresAt: testNow.Add(-time.Minute)},
			want:        protocol.AuthNotConnected,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &fakeStore{credential: testCase.credential, transaction: testCase.transaction}
			server := newAuthServer(t)
			clock := newFakeClock()
			manager := testManager(t, store, server, &fakeVerifier{}, clock)
			if manager.State() != testCase.want {
				t.Fatalf("state = %s, want %s", manager.State(), testCase.want)
			}
		})
	}
}

func TestPollTransientTransportKeepsTransaction(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	server.tokenStatuses = []int{http.StatusInternalServerError, http.StatusOK}
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if _, err := manager.Begin(context.Background()); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	state, err := manager.Poll(context.Background())
	if err != nil || state != protocol.AuthAuthorizing {
		t.Fatalf("transient poll = (%s, %v), want authorizing", state, err)
	}
	if store.transaction == nil {
		t.Fatal("transient failure must keep the transaction for the next poll")
	}
	state, err = manager.Poll(context.Background())
	if err != nil || state != protocol.AuthConnected {
		t.Fatalf("recovery poll = (%s, %v), want connected", state, err)
	}
}

func TestBeginRevalidatesExistingCredential(t *testing.T) {
	store := &fakeStore{credential: &Credential{
		AccessToken:          "tok",
		AccessTokenExpiresAt: testNow.Add(time.Hour),
	}}
	server := newAuthServer(t)
	clock := newFakeClock()
	verifier := &fakeVerifier{}
	manager := testManager(t, store, server, verifier, clock)

	challenge, err := manager.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if challenge != nil {
		t.Fatal("an already-connected credential must not start a device flow")
	}
	if manager.State() != protocol.AuthConnected {
		t.Fatalf("state = %s, want connected", manager.State())
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	deviceCodeCalls, _, _, _ := server.calls()
	if deviceCodeCalls != 0 {
		t.Fatalf("device code calls = %d, want 0", deviceCodeCalls)
	}
}

func TestNewManagerValidation(t *testing.T) {
	server := newAuthServer(t)
	clock := newFakeClock()
	base := Config{
		ClientID:      testClientID,
		RepositoryID:  testRepositoryID,
		Owner:         "neelbangera",
		Repo:          "lecture-transcripts",
		Branch:        "main",
		DeviceCodeURL: server.URL + "/login/device/code",
		TokenURL:      server.URL + "/login/oauth/access_token",
		Now:           clock.Now,
		Verifier:      &fakeVerifier{},
	}
	if _, err := NewManager(nil, base); err == nil {
		t.Fatal("expected nil store to be rejected")
	}
	missingClient := base
	missingClient.ClientID = ""
	if _, err := NewManager(&fakeStore{}, missingClient); err == nil {
		t.Fatal("expected missing client ID to be rejected")
	}
	missingRepo := base
	missingRepo.RepositoryID = 0
	if _, err := NewManager(&fakeStore{}, missingRepo); err == nil {
		t.Fatal("expected missing repository ID to be rejected")
	}
	missingBranch := base
	missingBranch.Branch = ""
	if _, err := NewManager(&fakeStore{}, missingBranch); err == nil {
		t.Fatal("expected missing branch to be rejected")
	}
}

func TestDeviceCodeTerminalErrorSurfacesReauthorization(t *testing.T) {
	store := &fakeStore{}
	server := newAuthServer(t)
	server.deviceCodeBody = `{"error":"device_flow_disabled"}`
	clock := newFakeClock()
	manager := testManager(t, store, server, &fakeVerifier{}, clock)

	if _, err := manager.Begin(context.Background()); !errors.Is(err, ErrReauthorizationRequired) {
		t.Fatalf("Begin error = %v, want ErrReauthorizationRequired", err)
	}
	if manager.State() != protocol.AuthReauthorizationRequired {
		t.Fatalf("state = %s, want reauthorization_required", manager.State())
	}
	if store.transaction != nil {
		t.Fatal("terminal device-code error must not persist a transaction")
	}
}
