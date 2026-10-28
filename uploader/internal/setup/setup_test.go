package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/auth"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
)

func TestParseRepositoryInput(t *testing.T) {
	valid := []struct {
		input string
		owner string
		repo  string
	}{
		{"octo/Hello-World", "octo", "Hello-World"},
		{"octo/hello-world", "octo", "hello-world"},
		{"https://github.com/octo/Hello-World", "octo", "Hello-World"},
		{"https://github.com/octo/Hello-World/", "octo", "Hello-World"},
		{"https://github.com/octocat/Hello-World", "octocat", "Hello-World"},
		{"  octo/repo  ", "octo", "repo"},
	}
	for _, v := range valid {
		owner, repo, err := ParseRepositoryInput(v.input)
		if err != nil {
			t.Fatalf("Parse(%q) = %v, want nil", v.input, err)
		}
		if owner != v.owner || repo != v.repo {
			t.Fatalf("Parse(%q) = %s/%s, want %s/%s", v.input, owner, repo, v.owner, v.repo)
		}
	}
	invalid := []string{
		"",
		"   ",
		"onlyowner",
		"a/b/c",
		"a//b",
		"https://github.com/onlyowner",
		"https://github.com/a/b/c",
		"https://github.com/a/b?x=1",
		"https://github.com/a/b#frag",
		"https://user:pass@github.com/a/b",
		"https://gitlab.com/a/b",
		"http://github.com/a/b",
		"https://github.com:8443/a/b",
		"https://github.com/a/b.git",
		"octo/repo.git",
		"octo/.",
		"octo/..",
		"own_er/repo",
		"-owner/repo",
		"owner/",
		"/repo",
		"owner/a/b",
		strings.Repeat("x", 600),
	}
	for _, input := range invalid {
		if _, _, err := ParseRepositoryInput(input); err == nil {
			t.Fatalf("Parse(%q) = nil, want error", input)
		}
	}
}

func TestDiscoverPublicID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/octo/Hello-World" {
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"id":123,"full_name":"octo/Hello-World"}`)
			return
		}
		if r.URL.Path == "/repos/octo/Private" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		if r.URL.Path == "/repos/octo/Mismatch" {
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"id":1,"full_name":"someone/else"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	id, err := discoverPublicID(context.Background(), server.Client(), server.URL, "octo", "Hello-World")
	if err != nil || id != 123 {
		t.Fatalf("public discover = (%d,%v), want (123,nil)", id, err)
	}
	if _, err := discoverPublicID(context.Background(), server.Client(), server.URL, "octo", "Private"); !strings.Contains(err.Error(), "") || err == nil {
		t.Fatalf("private should return errNotFound, got %v", err)
	} else if !isNotFound(err) {
		t.Fatalf("private should be errNotFound, got %v", err)
	}
	if _, err := discoverPublicID(context.Background(), server.Client(), server.URL, "octo", "Mismatch"); err == nil {
		t.Fatal("mismatched full_name must fail")
	}
}

func isNotFound(err error) bool {
	return err != nil && (err == errNotFound || strings.Contains(err.Error(), "not found"))
}

func TestCheckExistingState(t *testing.T) {
	home := t.TempDir()
	paths := config.PathsFor(home)
	write := func(cfg config.Config) {
		t.Helper()
		if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(paths.Config, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	valid := config.Config{SchemaVersion: 1, GitHubAppClientID: "Iv1.abc123def456", RepositoryID: 1, Owner: "octo", Repo: "repo", Branch: "main", WriteTimestamped: true}

	// No config, no queue: ok.
	if err := checkExistingState(paths, "Iv1.abc123def456", "octo", "repo"); err != nil {
		t.Fatalf("empty state = %v, want nil", err)
	}
	// Queue without config: blocked.
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Queue, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkExistingState(paths, "Iv1.abc123def456", "octo", "repo"); err == nil {
		t.Fatal("queue without config must be blocked")
	}
	os.Remove(paths.Queue)
	// Matching config: ok.
	write(valid)
	if err := checkExistingState(paths, "Iv1.abc123def456", "OCTO", "REPO"); err != nil {
		t.Fatalf("matching config (case-insensitive) = %v, want nil", err)
	}
	// Differing destination: blocked.
	if err := checkExistingState(paths, "Iv1.abc123def456", "octo", "other"); err == nil {
		t.Fatal("different repo must be blocked")
	}
	if err := checkExistingState(paths, "Iv1.different123", "octo", "repo"); err == nil {
		t.Fatal("different client ID must be blocked")
	}
}

func TestWriteConfigAtomically(t *testing.T) {
	home := t.TempDir()
	paths := config.PathsFor(home)
	cfg := config.Config{SchemaVersion: 1, GitHubAppClientID: "Iv1.abc123def456", RepositoryID: 42, Owner: "octo", Repo: "repo", Branch: "main", WriteTimestamped: false}
	if err := writeConfigAtomically(paths, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := config.LoadFrom(paths.Config)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.RepositoryID != 42 || loaded.WriteTimestamped {
		t.Fatalf("roundtrip = %+v", loaded)
	}
	info, err := os.Stat(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(paths.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
	entries, _ := os.ReadDir(paths.Dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	invalid := cfg
	invalid.RepositoryID = 0
	if err := writeConfigAtomically(paths, invalid); err == nil {
		t.Fatal("invalid config must be refused")
	}
}

// fakeStore is an in-memory auth.Store for setup tests.
type fakeStore struct {
	mu          sync.Mutex
	credential  *auth.Credential
	transaction *auth.DeviceTransaction
}

func (s *fakeStore) LoadCredential() (*auth.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credential == nil {
		return nil, auth.ErrNoCredential
	}
	c := *s.credential
	return &c, nil
}

func (s *fakeStore) SaveCredential(c auth.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := c
	s.credential = &cp
	return nil
}

func (s *fakeStore) DeleteCredential() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credential = nil
	return nil
}

func (s *fakeStore) LoadTransaction() (*auth.DeviceTransaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transaction == nil {
		return nil, auth.ErrNoTransaction
	}
	c := *s.transaction
	return &c, nil
}

func (s *fakeStore) SaveTransaction(tr auth.DeviceTransaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := tr
	s.transaction = &cp
	return nil
}

func (s *fakeStore) DeleteTransaction() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transaction = nil
	return nil
}

type setupServer struct {
	*httptest.Server
	mu         sync.Mutex
	tokenCalls int
}

func newSetupServer(t *testing.T, owner, repo string, id int64) *setupServer {
	t.Helper()
	s := &setupServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login/device/code":
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"device_code":"dev-secret","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`)
		case r.URL.Path == "/login/oauth/access_token":
			s.mu.Lock()
			s.tokenCalls++
			s.mu.Unlock()
			_ = r.ParseForm()
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"access_token":"ghu_setup","token_type":"bearer","expires_in":28800,"refresh_token":"ghr_setup","refresh_token_expires_in":15811200}`)
		case r.URL.Path == "/repos/"+owner+"/"+repo:
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"id":`+itoa(id)+`,"full_name":"`+owner+`/`+repo+`"}`)
		case r.URL.Path == "/repos/"+owner+"/"+repo+"/contents":
			if r.URL.Query().Get("ref") != "main" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func TestRunPublicRepositoryEndToEnd(t *testing.T) {
	owner, repo := "octo", "Hello-World"
	server := newSetupServer(t, owner, repo, 123)
	home := t.TempDir()
	store := &fakeStore{}
	var stdout bytes.Buffer
	opts := Options{
		AppClientID:   "Iv1.abc123def456",
		AppInstallURL: "https://github.com/apps/example/installations/new",
		Repository:    "https://github.com/" + owner + "/" + repo,
		APIBaseURL:    server.URL,
		DeviceCodeURL: server.URL + "/login/device/code",
		TokenURL:      server.URL + "/login/oauth/access_token",
		HTTPClient:    server.Client(),
		HomeDir:       home,
		Store:         store,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := Run(ctx, opts, strings.NewReader(""), &stdout); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Found repository ID 123") || !strings.Contains(out, "GitHub connected") || !strings.Contains(out, "Wrote ") {
		t.Fatalf("output missing expected steps:\n%s", out)
	}
	paths := config.PathsFor(home)
	loaded, err := config.LoadFrom(paths.Config)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if loaded.Owner != owner || loaded.Repo != repo || loaded.RepositoryID != 123 || loaded.GitHubAppClientID != "Iv1.abc123def456" || loaded.Branch != "main" {
		t.Fatalf("written config = %+v", loaded)
	}
	if store.credential == nil || store.credential.AccessToken != "ghu_setup" {
		t.Fatalf("publishing credential not stored: %+v", store.credential)
	}
	if store.credential.RepositoryID != 123 {
		t.Fatalf("stored credential repository ID = %d, want 123", store.credential.RepositoryID)
	}
}

func TestRunRefusesDestinationChange(t *testing.T) {
	home := t.TempDir()
	paths := config.PathsFor(home)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := config.Config{SchemaVersion: 1, GitHubAppClientID: "Iv1.abc123def456", RepositoryID: 1, Owner: "octo", Repo: "old", Branch: "main", WriteTimestamped: true}
	data, _ := json.Marshal(existing)
	if err := os.WriteFile(paths.Config, data, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		AppClientID: "Iv1.abc123def456",
		Repository:  "octo/new",
		HomeDir:     home,
		Store:       &fakeStore{},
		HTTPClient:  &http.Client{Timeout: 5 * time.Second},
		APIBaseURL:  "http://127.0.0.1:1",
	}
	var stdout bytes.Buffer
	if err := Run(context.Background(), opts, strings.NewReader(""), &stdout); err == nil {
		t.Fatal("destination change must be refused")
	} else if !strings.Contains(err.Error(), "reset") {
		t.Fatalf("error should mention reset, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.Dir, "queue.sqlite3")); !os.IsNotExist(err) {
		// No queue should have been created by a refused run.
		t.Fatalf("refused run must not create queue state")
	}
}
