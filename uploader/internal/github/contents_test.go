package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

const testTargetPath = "eecs491/006.md"

func testJob(hash string) protocol.TranscriptJob {
	return protocol.TranscriptJob{
		SchemaVersion: 1,
		Kind:          "lecture",
		LectureKey:    "eecs491/2026-winter/006",
		CourseSlug:    "eecs491",
		CourseName:    "EECS 491",
		Term:          "2026-winter",
		LectureNumber: 6,
		ContentHash:   hash,
	}
}

func renderedFile(hash string) []byte {
	return []byte("---\ncourse: 'EECS 491'\nterm: '2026-winter'\nlecture: 6\ntranscript_sha256: '" + hash + "'\n---\n\n## Transcript\n\nbody\n")
}

type recordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

func newFakeServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fakeServer {
	t.Helper()
	server := &fakeServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		server.mu.Lock()
		server.requests = append(server.requests, recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Header: r.Header.Clone(),
			Body:   body,
		})
		server.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *fakeServer) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

func (s *fakeServer) countMethod(method string) int {
	count := 0
	for _, request := range s.recorded() {
		if request.Method == method {
			count++
		}
	}
	return count
}

type fakeCredentials struct {
	header       string
	refreshed    string
	headerErr    error
	refreshErr   error
	headerCalls  int
	refreshCalls int
}

func (f *fakeCredentials) AuthorizationHeader(context.Context) (string, error) {
	f.headerCalls++
	if f.headerErr != nil {
		return "", f.headerErr
	}
	return f.header, nil
}

func (f *fakeCredentials) ForceRefresh(context.Context) (string, error) {
	f.refreshCalls++
	if f.refreshErr != nil {
		return "", f.refreshErr
	}
	if f.refreshed != "" {
		f.header = f.refreshed
	}
	return f.header, nil
}

func testClient(t *testing.T, baseURL string, credentials CredentialSource) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		BaseURL:        baseURL,
		Owner:          "neelbangera",
		Repo:           "lecture-transcripts",
		Branch:         "main",
		Credentials:    credentials,
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func contentsFile(t *testing.T, content []byte) map[string]any {
	t.Helper()
	return map[string]any{
		"type":     "file",
		"path":     testTargetPath,
		"sha":      "blob-sha",
		"size":     len(content),
		"encoding": "base64",
		"content":  base64.StdEncoding.EncodeToString(content),
	}
}

func asAPIError(t *testing.T, err error) *Error {
	t.Helper()
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not a *github.Error", err)
	}
	return apiErr
}

func TestPublishCreatesWithoutSHA(t *testing.T) {
	hash := strings.Repeat("a", 64)
	job := testJob(hash)
	rendered := renderedFile(hash)
	var putBody map[string]any
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			http.NotFound(w, r)
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			writeJSON(t, w, http.StatusCreated, map[string]any{
				"content": map[string]any{"path": testTargetPath, "sha": "blob-sha"},
				"commit":  map[string]any{"sha": "commit-sha"},
			})
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	credentials := &fakeCredentials{header: "Bearer test-token"}
	client := testClient(t, server.URL, credentials)

	result, err := client.Publish(context.Background(), testTargetPath, job, rendered)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Outcome != OutcomeCreated {
		t.Fatalf("outcome = %s, want created", result.Outcome)
	}
	if _, ok := putBody["sha"]; ok {
		t.Fatal("PUT must never include sha")
	}
	if got, want := putBody["message"], "Add EECS 491 lecture 6 (eecs491/006.md)"; got != want {
		t.Fatalf("commit message = %q, want %q", got, want)
	}
	if got := putBody["branch"]; got != "main" {
		t.Fatalf("branch = %v, want main", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(putBody["content"].(string))
	if err != nil || !bytes.Equal(decoded, rendered) {
		t.Fatalf("PUT content did not round-trip: %v", err)
	}
	if _, ok := putBody["author"]; ok {
		t.Fatal("PUT must not set author")
	}
	if _, ok := putBody["committer"]; ok {
		t.Fatal("PUT must not set committer")
	}

	requests := server.recorded()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if got := requests[0].Query.Get("ref"); got != "main" {
		t.Fatalf("GET ref = %q, want main", got)
	}
	if got := requests[0].Header.Get("Accept"); got != "application/vnd.github+json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := requests[0].Header.Get("X-GitHub-Api-Version"); got != "2026-03-10" {
		t.Fatalf("X-GitHub-Api-Version = %q", got)
	}
	if got := requests[0].Header.Get("Authorization"); got != "Bearer test-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestPublishUnchangedRevisit(t *testing.T) {
	hash := strings.Repeat("b", 64)
	job := testJob(hash)
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s request; unchanged must not PUT", r.Method)
		}
		writeJSON(t, w, http.StatusOK, contentsFile(t, renderedFile(hash)))
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})

	result, err := client.Publish(context.Background(), testTargetPath, job, renderedFile(hash))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Outcome != OutcomeUnchanged {
		t.Fatalf("outcome = %s, want unchanged", result.Outcome)
	}
	if result.Remote.ContentHash == nil || *result.Remote.ContentHash != hash {
		t.Fatalf("remote hash = %v, want %s", result.Remote.ContentHash, hash)
	}
	if server.countMethod(http.MethodPut) != 0 {
		t.Fatal("unchanged revisit must not issue a PUT")
	}
}

func TestPublishConflictOnDifferentHash(t *testing.T) {
	local := strings.Repeat("c", 64)
	remote := strings.Repeat("d", 64)
	job := testJob(local)
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, contentsFile(t, renderedFile(remote)))
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})

	result, err := client.Publish(context.Background(), testTargetPath, job, renderedFile(local))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Outcome != OutcomeConflict {
		t.Fatalf("outcome = %s, want conflict", result.Outcome)
	}
	if result.Remote.ContentHash == nil || *result.Remote.ContentHash != remote {
		t.Fatalf("remote hash = %v, want %s", result.Remote.ContentHash, remote)
	}
	if server.countMethod(http.MethodPut) != 0 {
		t.Fatal("conflict must not issue a PUT")
	}
}

func TestPublishConflictOnMalformedFile(t *testing.T) {
	hash := strings.Repeat("e", 64)
	job := testJob(hash)
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, contentsFile(t, []byte("no frontmatter here")))
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})

	result, err := client.Publish(context.Background(), testTargetPath, job, renderedFile(hash))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Outcome != OutcomeConflict {
		t.Fatalf("outcome = %s, want conflict", result.Outcome)
	}
	if result.Remote.Kind != protocol.RemoteMalformed {
		t.Fatalf("remote kind = %s, want malformed", result.Remote.Kind)
	}
	if result.Remote.ContentHash != nil {
		t.Fatal("malformed remote must not expose a trusted hash")
	}
}

func TestPublishConflictOnDirectoryArray(t *testing.T) {
	hash := strings.Repeat("f", 64)
	job := testJob(hash)
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, []map[string]any{{"type": "file", "path": testTargetPath + "/child.md"}})
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})

	result, err := client.Publish(context.Background(), testTargetPath, job, renderedFile(hash))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Outcome != OutcomeConflict || result.Remote.Kind != protocol.RemoteDirectory {
		t.Fatalf("result = %+v, want directory conflict", result)
	}
}

func TestPublishConflictOnSymlink(t *testing.T) {
	hash := strings.Repeat("1", 64)
	job := testJob(hash)
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"type": "symlink", "path": testTargetPath, "sha": "blob"})
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})

	result, err := client.Publish(context.Background(), testTargetPath, job, renderedFile(hash))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Outcome != OutcomeConflict || result.Remote.Kind != protocol.RemoteSymlink {
		t.Fatalf("result = %+v, want symlink conflict", result)
	}
}

func TestPublishRaceResolvesToUnchanged(t *testing.T) {
	hash := strings.Repeat("2", 64)
	job := testJob(hash)
	getCalls := 0
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				http.NotFound(w, r)
				return
			}
			writeJSON(t, w, http.StatusOK, contentsFile(t, renderedFile(hash)))
		case http.MethodPut:
			http.Error(w, "server error", http.StatusInternalServerError)
		}
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})

	result, err := client.Publish(context.Background(), testTargetPath, job, renderedFile(hash))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Outcome != OutcomeUnchanged {
		t.Fatalf("outcome = %s, want unchanged after race", result.Outcome)
	}
}

func TestPublishRaceResolvesToConflict(t *testing.T) {
	hash := strings.Repeat("3", 64)
	other := strings.Repeat("4", 64)
	job := testJob(hash)
	getCalls := 0
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				http.NotFound(w, r)
				return
			}
			writeJSON(t, w, http.StatusOK, contentsFile(t, renderedFile(other)))
		case http.MethodPut:
			http.Error(w, "server error", http.StatusInternalServerError)
		}
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})

	result, err := client.Publish(context.Background(), testTargetPath, job, renderedFile(hash))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Outcome != OutcomeConflict {
		t.Fatalf("outcome = %s, want conflict after race", result.Outcome)
	}
}

func TestPublishRaceStillAbsentReturnsClassifiedError(t *testing.T) {
	hash := strings.Repeat("5", 64)
	job := testJob(hash)
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})

	_, err := client.Publish(context.Background(), testTargetPath, job, renderedFile(hash))
	if err == nil {
		t.Fatal("expected classified error when the target stays absent")
	}
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryRetryable {
		t.Fatalf("category = %s, want retryable", apiErr.Category)
	}
}

func TestRateLimitClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header http.Header
		body   string
	}{
		{name: "429", status: http.StatusTooManyRequests},
		{name: "403 remaining zero", status: http.StatusForbidden, header: http.Header{"X-Ratelimit-Remaining": {"0"}}},
		{name: "403 retry after", status: http.StatusForbidden, header: http.Header{"Retry-After": {"30"}}},
		{name: "403 message", status: http.StatusForbidden, body: `{"message":"You have exceeded a secondary rate limit"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				for key, values := range testCase.header {
					w.Header()[key] = values
				}
				w.WriteHeader(testCase.status)
				io.WriteString(w, testCase.body)
			})
			client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})
			_, err := client.InspectFile(context.Background(), testTargetPath)
			apiErr := asAPIError(t, err)
			if apiErr.Category != CategoryRateLimit {
				t.Fatalf("category = %s, want rate_limit", apiErr.Category)
			}
			if !apiErr.Retryable() {
				t.Fatal("rate limit must be retryable")
			}
		})
	}
}

func TestAuthFailureRefreshesOnce(t *testing.T) {
	credentials := &fakeCredentials{header: "Bearer stale", refreshed: "Bearer fresh"}
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"Bad credentials"}`)
	})
	client := testClient(t, server.URL, credentials)

	_, err := client.InspectFile(context.Background(), testTargetPath)
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryAuth {
		t.Fatalf("category = %s, want auth", apiErr.Category)
	}
	if credentials.refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want exactly 1", credentials.refreshCalls)
	}
	if got := server.countMethod(http.MethodGet); got != 2 {
		t.Fatalf("GET attempts = %d, want 2 (original + one refresh retry)", got)
	}
	requests := server.recorded()
	if got := requests[1].Header.Get("Authorization"); got != "Bearer fresh" {
		t.Fatalf("retry Authorization = %q, want refreshed token", got)
	}
}

func TestAuthFailureWhenRefreshUnavailable(t *testing.T) {
	credentials := &fakeCredentials{header: "Bearer stale", refreshErr: errors.New("no refresh token")}
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	client := testClient(t, server.URL, credentials)

	_, err := client.InspectFile(context.Background(), testTargetPath)
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryAuth {
		t.Fatalf("category = %s, want auth", apiErr.Category)
	}
	if credentials.refreshCalls != 1 || server.countMethod(http.MethodGet) != 1 {
		t.Fatalf("refresh calls = %d, GETs = %d, want 1 and 1", credentials.refreshCalls, server.countMethod(http.MethodGet))
	}
}

func TestPermissionClassification(t *testing.T) {
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"message":"Resource not accessible by integration"}`)
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})
	_, err := client.InspectFile(context.Background(), testTargetPath)
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryPermission {
		t.Fatalf("category = %s, want permission", apiErr.Category)
	}
	if apiErr.Retryable() {
		t.Fatal("permission must not be retryable")
	}
}

func TestRepositoryNotFoundIsPermission(t *testing.T) {
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})
	_, err := client.GetRepository(context.Background())
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryPermission {
		t.Fatalf("category = %s, want permission", apiErr.Category)
	}
}

func TestTransientServerErrorIsRetryable(t *testing.T) {
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})
	_, err := client.InspectFile(context.Background(), testTargetPath)
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryRetryable || !apiErr.Retryable() {
		t.Fatalf("category = %s, want retryable", apiErr.Category)
	}
}

func TestNetworkFailureIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	baseURL := server.URL
	server.Close()

	client := testClient(t, baseURL, &fakeCredentials{header: "Bearer test-token"})
	_, err := client.InspectFile(context.Background(), testTargetPath)
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryRetryable {
		t.Fatalf("category = %s, want retryable", apiErr.Category)
	}
}

func TestTimeoutIsRetryable(t *testing.T) {
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "{}")
	})
	client, err := NewClient(ClientConfig{
		BaseURL:        server.URL,
		Owner:          "neelbangera",
		Repo:           "lecture-transcripts",
		Branch:         "main",
		Credentials:    &fakeCredentials{header: "Bearer test-token"},
		RequestTimeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.InspectFile(context.Background(), testTargetPath)
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryRetryable {
		t.Fatalf("category = %s, want retryable", apiErr.Category)
	}
}

func TestOversizedResponseIsPermanent(t *testing.T) {
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, strings.Repeat("x", 4096))
	})
	client, err := NewClient(ClientConfig{
		BaseURL:          server.URL,
		Owner:            "neelbangera",
		Repo:             "lecture-transcripts",
		Branch:           "main",
		Credentials:      &fakeCredentials{header: "Bearer test-token"},
		MaxResponseBytes: 128,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.InspectFile(context.Background(), testTargetPath)
	apiErr := asAPIError(t, err)
	if apiErr.Category != CategoryPermanent {
		t.Fatalf("category = %s, want permanent", apiErr.Category)
	}
}

func TestNonCreatedUpdateResponseIsRejected(t *testing.T) {
	server := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"content": map[string]any{"sha": "blob"}})
	})
	client := testClient(t, server.URL, &fakeCredentials{header: "Bearer test-token"})
	_, err := client.CreateFile(context.Background(), testTargetPath, []byte("body"), "message")
	if err == nil {
		t.Fatal("expected 200 update response to be rejected as non-created")
	}
}

func TestProtocolCategoryMapping(t *testing.T) {
	cases := map[Category]protocol.ErrorCategory{
		CategoryAuth:       protocol.ErrorReauthorizationRequired,
		CategoryPermission: protocol.ErrorRejectedPermission,
		CategoryRetryable:  protocol.ErrorInternal,
		CategoryRateLimit:  protocol.ErrorInternal,
		CategoryPermanent:  protocol.ErrorInternal,
	}
	for category, want := range cases {
		if got := category.ProtocolCategory(); got != want {
			t.Fatalf("%s.ProtocolCategory() = %s, want %s", category, got, want)
		}
	}
}

func TestErrorStringIsSanitized(t *testing.T) {
	apiErr := &Error{Category: CategoryRetryable, HTTPStatus: 500, Op: "contents.get"}
	if strings.Contains(apiErr.Error(), "token") || strings.Contains(apiErr.Error(), "http://") {
		t.Fatalf("error string leaked transport detail: %s", apiErr.Error())
	}
}

func TestParseTranscriptHash(t *testing.T) {
	hash := strings.Repeat("a", 64)
	cases := []struct {
		name    string
		content string
		want    string
		ok      bool
	}{
		{name: "single quoted", content: "---\ntranscript_sha256: '" + hash + "'\n---\nbody", want: hash, ok: true},
		{name: "bare", content: "---\ntranscript_sha256: " + hash + "\n---\nbody", want: hash, ok: true},
		{name: "double quoted", content: "---\ntranscript_sha256: \"" + hash + "\"\n---\n", want: hash, ok: true},
		{name: "crlf", content: "---\r\ntranscript_sha256: '" + hash + "'\r\n---\r\nbody", want: hash, ok: true},
		{name: "no frontmatter", content: "body only", ok: false},
		{name: "missing closing", content: "---\ntranscript_sha256: '" + hash + "'\n", ok: false},
		{name: "duplicate", content: "---\ntranscript_sha256: '" + hash + "'\ntranscript_sha256: '" + hash + "'\n---\n", ok: false},
		{name: "invalid hash", content: "---\ntranscript_sha256: 'nothex'\n---\n", ok: false},
		{name: "no hash", content: "---\ncourse: 'EECS 491'\n---\n", ok: false},
		{name: "empty", content: "", ok: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := ParseTranscriptHash([]byte(testCase.content))
			if ok != testCase.ok || got != testCase.want {
				t.Fatalf("ParseTranscriptHash = (%q, %v), want (%q, %v)", got, ok, testCase.want, testCase.ok)
			}
		})
	}
}

func TestNewClientRejectsInsecureBaseURL(t *testing.T) {
	if _, err := NewClient(ClientConfig{BaseURL: "http://example.com", Owner: "o", Repo: "r", Branch: "main"}); err == nil {
		t.Fatal("expected non-loopback http base URL to be rejected")
	}
	if _, err := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:1", Owner: "o", Repo: "r", Branch: "main"}); err != nil {
		t.Fatalf("loopback http should be allowed for tests: %v", err)
	}
	if _, err := NewClient(ClientConfig{BaseURL: "https://api.github.com", Owner: "", Repo: "r", Branch: "main"}); err == nil {
		t.Fatal("expected missing owner to be rejected")
	}
}

func TestCommitMessage(t *testing.T) {
	job := testJob(strings.Repeat("a", 64))
	if got, want := CommitMessage(job, testTargetPath), "Add EECS 491 lecture 6 (eecs491/006.md)"; got != want {
		t.Fatalf("CommitMessage = %q, want %q", got, want)
	}
}
