package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public GitHub REST API.
	DefaultBaseURL = "https://api.github.com"
	// DefaultRequestTimeout bounds one Contents/Repository request.  It is
	// deliberately well below the ten-minute queue lease.
	DefaultRequestTimeout = 30 * time.Second
	// DefaultMaxResponseBytes caps a decoded GitHub response body.
	DefaultMaxResponseBytes = 1 << 20

	apiVersion   = "2026-03-10"
	acceptHeader = "application/vnd.github+json"
	userAgent    = "lecture-transcripts-uploader"
)

// CredentialSource supplies a fresh Authorization header value.  The auth
// package implements it; the GitHub client never stores or inspects the raw
// credential itself.
type CredentialSource interface {
	// AuthorizationHeader returns "Bearer <token>", refreshing first when the
	// stored access token is near expiry.
	AuthorizationHeader(ctx context.Context) (string, error)
	// ForceRefresh refreshes the credential exactly once after a 401.  It
	// returns an error when no refresh is possible.
	ForceRefresh(ctx context.Context) (string, error)
}

// ClientConfig configures a Client.  Tests point BaseURL at an httptest
// server; production uses DefaultBaseURL.
type ClientConfig struct {
	BaseURL          string
	Owner            string
	Repo             string
	Branch           string
	Credentials      CredentialSource
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MaxResponseBytes int64
}

// Client performs authenticated, bounded GitHub REST requests.
type Client struct {
	baseURL          *url.URL
	owner            string
	repo             string
	branch           string
	credentials      CredentialSource
	httpClient       *http.Client
	requestTimeout   time.Duration
	maxResponseBytes int64
}

// NewClient validates the configuration and returns a client.  Plain HTTP is
// accepted only for loopback hosts so tests never require network access.
func NewClient(cfg ClientConfig) (*Client, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("github: invalid API base URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("github: API base URL must use https")
	}
	if strings.TrimSpace(cfg.Owner) == "" || strings.TrimSpace(cfg.Repo) == "" {
		return nil, errors.New("github: owner and repo are required")
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		return nil, errors.New("github: branch is required")
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	maxBytes := cfg.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{
		baseURL:          parsed,
		owner:            cfg.Owner,
		repo:             cfg.Repo,
		branch:           cfg.Branch,
		credentials:      cfg.Credentials,
		httpClient:       httpClient,
		requestTimeout:   timeout,
		maxResponseBytes: maxBytes,
	}, nil
}

// Branch returns the configured branch.  It is only ever encoded as a query
// or body value, never concatenated into a URL path.
func (c *Client) Branch() string { return c.branch }

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

// do performs one logical request, forcing at most one credential refresh
// after a 401.  A 401 with the refreshed credential is returned to the caller
// and classified as CategoryAuth.
func (c *Client) do(ctx context.Context, method, rawPath string, query url.Values, payload any) (apiResponse, error) {
	response, err := c.once(ctx, method, rawPath, query, payload)
	if err != nil {
		return apiResponse{}, err
	}
	if response.status == http.StatusUnauthorized && c.credentials != nil {
		if _, refreshErr := c.credentials.ForceRefresh(ctx); refreshErr != nil {
			return apiResponse{}, newError(opName(method, rawPath), CategoryAuth, http.StatusUnauthorized)
		}
		retried, retryErr := c.once(ctx, method, rawPath, query, payload)
		if retryErr != nil {
			return apiResponse{}, retryErr
		}
		return retried, nil
	}
	return response, nil
}

func (c *Client) once(ctx context.Context, method, rawPath string, query url.Values, payload any) (apiResponse, error) {
	op := opName(method, rawPath)

	target := *c.baseURL
	target.Path = strings.TrimSuffix(target.Path, "/") + rawPath
	if query != nil {
		target.RawQuery = query.Encode()
	}

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return apiResponse{}, newError(op, CategoryPermanent, 0)
		}
		body = bytes.NewReader(encoded)
	}

	requestCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, target.String(), body)
	if err != nil {
		return apiResponse{}, newError(op, CategoryPermanent, 0)
	}
	request.Header.Set("Accept", acceptHeader)
	request.Header.Set("X-GitHub-Api-Version", apiVersion)
	request.Header.Set("User-Agent", userAgent)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.credentials != nil {
		header, err := c.credentials.AuthorizationHeader(ctx)
		if err != nil {
			return apiResponse{}, newError(op, CategoryAuth, 0)
		}
		request.Header.Set("Authorization", header)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		// Timeouts, cancellation, DNS, and connection failures are all
		// retryable and must not leak the underlying error text.
		return apiResponse{}, newError(op, CategoryRetryable, 0)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if err != nil {
		return apiResponse{}, newError(op, CategoryRetryable, response.StatusCode)
	}
	if int64(len(data)) > c.maxResponseBytes {
		return apiResponse{}, newError(op, CategoryPermanent, response.StatusCode)
	}
	return apiResponse{status: response.StatusCode, header: response.Header.Clone(), body: data}, nil
}

func opName(method, rawPath string) string {
	switch {
	case method == http.MethodGet && strings.Contains(rawPath, "/contents"):
		return "contents.get"
	case method == http.MethodPut && strings.Contains(rawPath, "/contents"):
		return "contents.put"
	case method == http.MethodGet && strings.Contains(rawPath, "/repos/"):
		return "repos.get"
	default:
		return "request"
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Repository is the bounded subset of GET /repos/{owner}/{repo} the uploader
// verifies against configuration.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

// GetRepository reads repository identity metadata.
func (c *Client) GetRepository(ctx context.Context) (Repository, error) {
	response, err := c.do(ctx, http.MethodGet, c.repoPath(), nil, nil)
	if err != nil {
		return Repository{}, err
	}
	if response.status != http.StatusOK {
		return Repository{}, classifyHTTP("repos.get", response.status, response.header, response.body)
	}
	var repository Repository
	if err := json.Unmarshal(response.body, &repository); err != nil || repository.ID <= 0 || repository.FullName == "" {
		return Repository{}, newError("repos.get", CategoryPermanent, response.status)
	}
	return repository, nil
}

func (c *Client) repoPath() string {
	return "/repos/" + url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo)
}

// contentsPath escapes every segment of a repository-relative path.
func (c *Client) contentsPath(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return c.repoPath() + "/contents"
	}
	segments := strings.Split(trimmed, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return c.repoPath() + "/contents/" + strings.Join(segments, "/")
}
