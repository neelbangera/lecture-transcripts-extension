// Package setup implements the guided first-time configuration for the
// Lecture Transcripts macOS uploader (`lecture-uploader --setup`).
//
// It parses a user-supplied repository destination, discovers the numeric
// repository ID (public via unauthenticated metadata, private via a temporary
// discovery authorization that is never persisted), obtains a
// repository-restricted publishing credential through the GitHub App Device
// Flow, verifies Contents access on `main`, and writes the complete
// machine-local config atomically. It never writes a partial config and never
// stores the temporary discovery credential.
//
// Discovery contract (D1 gate, verified 2026-10-05 against the current GitHub
// docs):
//
//   - `GET /repos/{owner}/{repo}` returns `id` and `full_name` for public
//     repositories without authentication.
//   - Private repositories return 404 without authentication, so an
//     authenticated lookup is required.
//   - The device-flow token endpoint accepts an optional `repository_id` that
//     restricts the resulting user access token further. A token obtained
//     without `repository_id` is broader (all installations the user can
//     access) and must only be used for discovery, never for publishing, and
//     must never become the stored Keychain credential.
//   - The final publishing token always requests `repository_id` and is stored
//     via auth.Manager, which verifies numeric ID, full name, and readable
//     Contents on `main` before reporting connected.
//   - Server-side revocation (`DELETE /applications/{client_id}/token`) requires
//     Basic auth with client_id + client_secret. A public device-flow client
//     ships no secret, so the uploader cannot revoke the temporary discovery
//     token server-side. The token is kept in memory only, cleared after use,
//     expires automatically (8h access, 6mo refresh), and the user can revoke it
//     manually at GitHub Settings → Applications → Authorized GitHub Apps.
//     Deleting local state must never be described as remote revocation.
package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/auth"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
)

const (
	// DefaultAPIBaseURL is the public GitHub REST API.
	DefaultAPIBaseURL = "https://api.github.com"
	// DefaultDeviceCodeURL starts the GitHub App Device Flow.
	DefaultDeviceCodeURL = "https://github.com/login/device/code"
	// DefaultTokenURL exchanges an approved device code for tokens.
	DefaultTokenURL = "https://github.com/login/oauth/access_token"

	requestTimeout     = 30 * time.Second
	maxResponseBytes   = 64 << 10
	maxInputBytes      = 4 << 10
	githubAPIVersion   = "2026-03-10"
	githubAccept       = "application/vnd.github+json"
	userAgent          = "lecture-transcripts-uploader"
	deviceGrantType    = "urn:ietf:params:oauth:grant-type:device_code"
	defaultPollSeconds = 5
)

// Options configures one setup run. AppClientID, AppInstallURL, and Repository
// normally come from `--app-client-id`, `--app-install-url`, and `--repository`
// flags. The remaining fields are test overrides; production leaves them empty
// so Run resolves the public defaults, the real home directory, a bounded HTTP
// client, and the macOS Keychain store.
type Options struct {
	AppClientID  string
	AppInstallURL string
	Repository   string

	APIBaseURL    string
	DeviceCodeURL string
	TokenURL      string
	HTTPClient    *http.Client
	HomeDir       string
	Store         auth.Store
}

// Run executes guided setup. Human-readable prompts go to stdout; nothing
// framed for Native Messaging is written here. The caller dispatches `--setup`
// before any protocol I/O (see cmd/lecture-uploader).
func Run(ctx context.Context, opts Options, stdin io.Reader, stdout io.Writer) error {
	out := stdout
	if out == nil {
		out = io.Discard
	}
	clientID := strings.TrimSpace(opts.AppClientID)
	if clientID == "" {
		line, err := promptLine(ctx, stdin, out, "GitHub App client ID: ")
		if err != nil {
			return err
		}
		clientID = strings.TrimSpace(line)
	}
	if err := config.ValidateClientID(clientID); err != nil {
		return fmt.Errorf("invalid app client ID: %w", err)
	}
	repoInput := strings.TrimSpace(opts.Repository)
	if repoInput == "" {
		line, err := promptLine(ctx, stdin, out, "Repository (OWNER/REPO or https://github.com/OWNER/REPO): ")
		if err != nil {
			return err
		}
		repoInput = strings.TrimSpace(line)
	}
	owner, repo, err := ParseRepositoryInput(repoInput)
	if err != nil {
		return err
	}
	home := strings.TrimSpace(opts.HomeDir)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return errors.New("resolve home directory: unavailable")
		}
	}
	paths := config.PathsFor(home)
	apiBase := strings.TrimSpace(opts.APIBaseURL)
	if apiBase == "" {
		apiBase = DefaultAPIBaseURL
	}
	deviceCodeURL := strings.TrimSpace(opts.DeviceCodeURL)
	if deviceCodeURL == "" {
		deviceCodeURL = DefaultDeviceCodeURL
	}
	tokenURL := strings.TrimSpace(opts.TokenURL)
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}

	if err := checkExistingState(paths, clientID, owner, repo); err != nil {
		return err
	}

	fmt.Fprintf(out, "Looking up %s/%s…\n", owner, repo)
	repositoryID, err := discoverPublicID(ctx, httpClient, apiBase, owner, repo)
	if err != nil {
		if errors.Is(err, errNotFound) {
			fmt.Fprintln(out, "Repository is not publicly visible. Starting private discovery.")
			fmt.Fprintln(out, "This uses one temporary authorization for lookup only.")
			fmt.Fprintln(out, "It cannot publish and is never saved; you will authorize once more for publishing.")
			repositoryID, err = discoverPrivateID(ctx, httpClient, apiBase, deviceCodeURL, tokenURL, clientID, owner, repo, stdin, out)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	fmt.Fprintf(out, "Found repository ID %d for %s/%s.\n", repositoryID, owner, repo)

	if installURL := strings.TrimSpace(opts.AppInstallURL); installURL != "" {
		fmt.Fprintf(out, "\nInstall the GitHub App on only %s/%s first:\n  %s\n", owner, repo, installURL)
		fmt.Fprintln(out, "Grant access to only the selected repository, then continue.")
	} else {
		fmt.Fprintf(out, "\nIn GitHub, install the maintainer-provided App on only %s/%s.\n", owner, repo)
	}

	credentialStore := opts.Store
	if credentialStore == nil {
		var err error
		credentialStore, err = auth.NewStore()
		if err != nil {
			return fmt.Errorf("credential storage requires macOS with cgo enabled: %w", err)
		}
	}
	manager, err := auth.NewManager(credentialStore, auth.Config{
		ClientID:      clientID,
		RepositoryID:  repositoryID,
		Owner:         owner,
		Repo:          repo,
		Branch:        config.ExpectedBranch,
		DeviceCodeURL: deviceCodeURL,
		TokenURL:      tokenURL,
		APIBaseURL:    apiBase,
		HTTPClient:    httpClient,
	})
	if err != nil {
		return fmt.Errorf("configure authorization: %w", err)
	}
	if err := runPublishingFlow(ctx, manager, out); err != nil {
		return err
	}

	cfg := config.Config{
		SchemaVersion:     config.SchemaVersion,
		GitHubAppClientID: clientID,
		RepositoryID:      repositoryID,
		Owner:             owner,
		Repo:              repo,
		Branch:            config.ExpectedBranch,
		WriteTimestamped:  true,
	}
	// Preserve an explicit writeTimestamped=false from a same-destination
	// reinstall instead of silently re-enabling timestamped uploads.
	if existing, err := config.LoadFrom(paths.Config); err == nil &&
		strings.EqualFold(existing.Owner, owner) &&
		strings.EqualFold(existing.Repo, repo) &&
		existing.RepositoryID == repositoryID &&
		existing.GitHubAppClientID == clientID {
		cfg.WriteTimestamped = existing.WriteTimestamped
	}
	if err := writeConfigAtomically(paths, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nWrote %s\n", paths.Config)
	fmt.Fprintln(out, "Next: register the Chrome host with your loaded extension ID,")
	fmt.Fprintln(out, "restart Chrome, open the extension, and verify a real capture.")
	return nil
}

// ParseRepositoryInput accepts a GitHub repository URL or OWNER/REPO and
// returns the validated owner and repo names. It rejects credentials in URLs,
// non-github.com hosts, extra path/query/fragment components, traversal, and
// malformed names rather than interpreting them as destinations.
func ParseRepositoryInput(input string) (owner, repo string, err error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", "", errors.New("repository is required: use OWNER/REPO or https://github.com/OWNER/REPO")
	}
	if len(trimmed) > 512 {
		return "", "", errors.New("repository input is too long")
	}
	if strings.Contains(trimmed, "://") {
		parsed, parseErr := url.Parse(trimmed)
		if parseErr != nil {
			return "", "", errors.New("repository URL is malformed")
		}
		if !strings.EqualFold(parsed.Scheme, "https") {
			return "", "", errors.New("repository URL must use https")
		}
		if parsed.User != nil {
			return "", "", errors.New("repository URL must not contain credentials")
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "github.com" {
			return "", "", errors.New("repository URL must be on github.com")
		}
		if port := parsed.Port(); port != "" && port != "443" {
			return "", "", errors.New("repository URL must not specify a port")
		}
		if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
			return "", "", errors.New("repository URL must not contain query or fragment; use OWNER/REPO or https://github.com/OWNER/REPO")
		}
		segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
		// strings.Trim("/", "/") is empty; Split returns [""], reject below.
		cleaned := make([]string, 0, len(segments))
		for _, seg := range segments {
			if seg == "" {
				continue
			}
			decoded, decErr := url.PathUnescape(seg)
			if decErr != nil {
				return "", "", errors.New("repository URL path is malformed")
			}
			cleaned = append(cleaned, decoded)
		}
		if len(cleaned) != 2 {
			return "", "", errors.New("repository URL must be exactly https://github.com/OWNER/REPO")
		}
		owner, repo = cleaned[0], cleaned[1]
	} else {
		if strings.ContainsAny(trimmed, "?#@\\") {
			return "", "", errors.New("repository must be OWNER/REPO without query, fragment, or credentials")
		}
		parts := strings.Split(trimmed, "/")
		if len(parts) != 2 {
			return "", "", errors.New("repository must be OWNER/REPO or https://github.com/OWNER/REPO")
		}
		owner, repo = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if strings.HasSuffix(strings.ToLower(repo), ".git") {
		return "", "", errors.New("repository must not end in .git; use OWNER/REPO or the https web URL")
	}
	if err := config.ValidateRepositoryName(owner, repo); err != nil {
		return "", "", fmt.Errorf("invalid repository: %w", err)
	}
	return owner, repo, nil
}

var errNotFound = errors.New("repository not found")

type repoMetadata struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}

// discoverPublicID performs an unauthenticated GET /repos/{owner}/{repo}.
// It succeeds for public repositories and returns errNotFound for anything
// else that hides existence (private, missing, or inaccessible), so the caller
// can fall back to authenticated discovery.
func discoverPublicID(ctx context.Context, client *http.Client, apiBase, owner, repo string) (int64, error) {
	endpoint := strings.TrimRight(apiBase, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	body, status, err := getJSON(ctx, client, endpoint, "")
	if err != nil {
		return 0, err
	}
	switch status {
	case http.StatusOK:
		var meta repoMetadata
		if err := json.Unmarshal(body, &meta); err != nil || meta.ID <= 0 || meta.FullName == "" {
			return 0, errors.New("repository lookup returned an invalid response")
		}
		if !strings.EqualFold(meta.FullName, owner+"/"+repo) {
			return 0, errors.New("repository lookup returned a different full name")
		}
		return meta.ID, nil
	case http.StatusNotFound, http.StatusForbidden, http.StatusUnauthorized:
		return 0, errNotFound
	default:
		if status == http.StatusMovedPermanently || status == http.StatusFound {
			return 0, errors.New("repository lookup was redirected; use OWNER/REPO for the current location")
		}
		return 0, fmt.Errorf("repository lookup failed with status %d", status)
	}
}

// discoverPrivateID runs one temporary device authorization without
// repository_id, uses the in-memory token for a single authenticated
// GET /repos/{owner}/{repo}, then discards the token. The token is never
// written to Keychain or disk.
func discoverPrivateID(ctx context.Context, client *http.Client, apiBase, deviceCodeURL, tokenURL, clientID, owner, repo string, stdin io.Reader, stdout io.Writer) (int64, error) {
	code, err := requestDeviceCode(ctx, client, deviceCodeURL, clientID)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(stdout, "\nEnter this code at %s:\n  %s\n", code.VerificationURI, code.UserCode)
	if code.VerificationURIComplete != "" {
		fmt.Fprintf(stdout, "Or open directly:\n  %s\n", code.VerificationURIComplete)
	}
	fmt.Fprintln(stdout, "Waiting for approval (lookup only; nothing is published)…")
	token, err := pollForToken(ctx, client, tokenURL, clientID, code.DeviceCode, "", code.Interval, code.ExpiresAt)
	if err != nil {
		return 0, err
	}
	// Best-effort memory hygiene: the temporary token lives only in this
	// function and is cleared before return. It cannot be revoked server-side
	// by a public client, so it expires automatically; see the package comment.
	defer func() { token = "" }()
	if strings.TrimSpace(token) == "" {
		return 0, errors.New("discovery authorization did not return a token")
	}
	endpoint := strings.TrimRight(apiBase, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	body, status, err := getJSON(ctx, client, endpoint, token)
	token = ""
	if err != nil {
		return 0, err
	}
	switch status {
	case http.StatusOK:
		var meta repoMetadata
		if err := json.Unmarshal(body, &meta); err != nil || meta.ID <= 0 || meta.FullName == "" {
			return 0, errors.New("repository lookup returned an invalid response")
		}
		if !strings.EqualFold(meta.FullName, owner+"/"+repo) {
			return 0, errors.New("repository lookup returned a different full name")
		}
		return meta.ID, nil
	case http.StatusNotFound, http.StatusForbidden:
		return 0, errors.New("repository is inaccessible with this account: install the App on the selected repository and use an account with access, then retry")
	case http.StatusUnauthorized:
		return 0, errors.New("discovery authorization was rejected; run setup again")
	default:
		return 0, fmt.Errorf("repository lookup failed with status %d", status)
	}
}

// runPublishingFlow drives the repository-restricted device flow via
// auth.Manager. A nil challenge means an existing credential already verified;
// otherwise the user code is displayed and polling continues until connected,
// canceled, expired, or the App installation is missing.
func runPublishingFlow(ctx context.Context, manager *auth.Manager, stdout io.Writer) error {
	challenge, err := manager.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start authorization: %w", err)
	}
	if challenge == nil {
		fmt.Fprintln(stdout, "GitHub authorization is already connected for this repository.")
		return nil
	}
	fmt.Fprintf(stdout, "\nEnter this code at %s:\n  %s\n", challenge.VerificationURI, challenge.UserCode)
	if challenge.VerificationURIComplete != "" {
		fmt.Fprintf(stdout, "Or open directly:\n  %s\n", challenge.VerificationURIComplete)
	}
	fmt.Fprintln(stdout, "Waiting for GitHub approval…")
	for {
		state, pollErr := manager.Poll(ctx)
		if pollErr != nil {
			return fmt.Errorf("authorization failed: %w", pollErr)
		}
		switch state {
		case "connected":
			fmt.Fprintln(stdout, "GitHub connected: repository verification passed on main.")
			return nil
		case "authorizing":
			interval := manager.Challenge().Interval
			if interval <= 0 {
				interval = defaultPollSeconds * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		default:
			return fmt.Errorf("authorization ended in state %s", string(state))
		}
	}
}

// checkExistingState refuses to redirect an existing installation. A matching
// config may be re-verified; a differing destination, or a queue file without a
// matching config, requires an explicit reset that also clears pending
// extension handoffs before a new destination is accepted.
func checkExistingState(paths config.Paths, clientID, owner, repo string) error {
	existing, err := config.LoadFrom(paths.Config)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			// Preserve the wrapped fail-closed reason without leaking paths
			// beyond what the operator already supplied.
			return fmt.Errorf("existing config is unreadable; reset the installation before changing destination: %w", err)
		}
		// No config yet: a leftover queue would be redirected silently, so
		// require a reset first.
		if queueExists(paths.Queue) {
			return errors.New("a queue file exists without matching config; drain it to the old repository or reset the installation (clearing uploader state and pending extension handoffs) before configuring a new destination")
		}
		return nil
	}
	if strings.EqualFold(existing.Owner, owner) &&
		strings.EqualFold(existing.Repo, repo) &&
		existing.GitHubAppClientID == clientID &&
		existing.Branch == config.ExpectedBranch {
		if existing.RepositoryID <= 0 {
			return errors.New("existing config is invalid; reset the installation before continuing")
		}
		return nil
	}
	return fmt.Errorf("existing installation targets %s/%s; destination or App changes require an explicit reset that clears uploader state and pending extension handoffs before a new destination is accepted", existing.Owner, existing.Repo)
}

func queueExists(queuePath string) bool {
	info, err := os.Stat(queuePath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// writeConfigAtomically creates the data directory (0700) and writes the
// complete config via temp-file rename with mode 0600. It is only called after
// successful verification, so no partial config is ever activated.
func writeConfigAtomically(paths config.Paths, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("refusing to write invalid config: %w", err)
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(paths.Dir, "config-*.tmp")
	if err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("stage config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("stage config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("stage config: %w", err)
	}
	if err := os.Rename(tmpName, paths.Config); err != nil {
		return fmt.Errorf("activate config: %w", err)
	}
	if err := os.Chmod(paths.Config, 0o600); err != nil {
		return fmt.Errorf("secure config: %w", err)
	}
	// Keep the directory itself private even when it already existed.
	_ = os.Chmod(paths.Dir, 0o700)
	_ = filepath.Clean(paths.Config)
	return nil
}

func promptLine(ctx context.Context, stdin io.Reader, stdout io.Writer, prompt string) (string, error) {
	if stdin == nil {
		return "", errors.New("missing required input: run with explicit flags in non-interactive mode")
	}
	fmt.Fprint(stdout, prompt)
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		reader := bufio.NewReader(io.LimitReader(stdin, maxInputBytes+1024))
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			ch <- result{"", err}
			return
		}
		line = strings.TrimSpace(line)
		if len(line) > maxInputBytes {
			ch <- result{"", errors.New("input is too long")}
			return
		}
		if line == "" {
			ch <- result{"", errors.New("missing required input")}
			return
		}
		ch <- result{line, nil}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return r.line, r.err
	}
}

type deviceCode struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	Interval                time.Duration
	ExpiresAt               time.Time
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
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
	Interval    int    `json:"interval"`
}

func requestDeviceCode(ctx context.Context, client *http.Client, deviceCodeURL, clientID string) (*deviceCode, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	body, err := postForm(ctx, client, deviceCodeURL, form)
	if err != nil {
		return nil, err
	}
	var resp deviceCodeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, errors.New("authorization service returned an invalid response")
	}
	if resp.Error != "" {
		return nil, errors.New("authorization service refused the device request; confirm Device Flow is enabled for the App")
	}
	if resp.DeviceCode == "" || resp.UserCode == "" || resp.VerificationURI == "" || resp.ExpiresIn <= 0 {
		return nil, errors.New("authorization service returned an incomplete challenge")
	}
	interval := time.Duration(resp.Interval) * time.Second
	if interval <= 0 {
		interval = defaultPollSeconds * time.Second
	}
	return &deviceCode{
		DeviceCode:              resp.DeviceCode,
		UserCode:                resp.UserCode,
		VerificationURI:         resp.VerificationURI,
		VerificationURIComplete: resp.VerificationURIComplete,
		Interval:                interval,
		ExpiresAt:               time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second),
	}, nil
}

// pollForToken polls the token endpoint until a token is issued, the device
// code expires, the user denies access, or the context is canceled.
// repositoryID is empty for discovery and set for publishing-style callers;
// this helper is only used for discovery (empty), while publishing goes
// through auth.Manager so the Keychain transaction stays resumable.
func pollForToken(ctx context.Context, client *http.Client, tokenURL, clientID, deviceCode, repositoryID string, interval time.Duration, expiresAt time.Time) (string, error) {
	if interval <= 0 {
		interval = defaultPollSeconds * time.Second
	}
	for {
		if !time.Now().Before(expiresAt) {
			return "", errors.New("authorization expired before approval; run setup again")
		}
		form := url.Values{}
		form.Set("client_id", clientID)
		form.Set("device_code", deviceCode)
		form.Set("grant_type", deviceGrantType)
		if repositoryID != "" {
			form.Set("repository_id", repositoryID)
		}
		body, err := postForm(ctx, client, tokenURL, form)
		if err != nil {
			// Transport failures are transient; keep polling until expiry.
			if sleepCtx(ctx, interval) != nil {
				return "", ctx.Err()
			}
			continue
		}
		var resp tokenResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			if sleepCtx(ctx, interval) != nil {
				return "", ctx.Err()
			}
			continue
		}
		if resp.Error != "" {
			switch resp.Error {
			case "authorization_pending":
				if sleepCtx(ctx, interval) != nil {
					return "", ctx.Err()
				}
				continue
			case "slow_down":
				if resp.Interval > 0 {
					interval = time.Duration(resp.Interval) * time.Second
				} else {
					interval += 5 * time.Second
				}
				if sleepCtx(ctx, interval) != nil {
					return "", ctx.Err()
				}
				continue
			case "expired_token":
				return "", errors.New("authorization expired before approval; run setup again")
			case "access_denied", "incorrect_device_code":
				return "", errors.New("authorization was denied; run setup again to retry")
			case "installation_missing_access":
				return "", errors.New("the App is not installed on the selected repository; install it on only that repository, then retry")
			default:
				return "", errors.New("authorization failed; run setup again")
			}
		}
		if resp.AccessToken == "" {
			return "", errors.New("authorization service returned an empty token")
		}
		return resp.AccessToken, nil
	}
}

func getJSON(ctx context.Context, client *http.Client, endpoint, bearer string) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, errors.New("repository lookup request failed")
	}
	req.Header.Set("Accept", githubAccept)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	req.Header.Set("User-Agent", userAgent)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("repository lookup transport failure")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || int64(len(body)) > maxResponseBytes {
		return nil, 0, errors.New("repository lookup response was unreadable")
	}
	return body, resp.StatusCode, nil
}

func postForm(ctx context.Context, client *http.Client, endpoint string, form url.Values) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("authorization transport failure")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("authorization transport failure")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || int64(len(body)) > maxResponseBytes {
		return nil, errors.New("authorization transport failure")
	}
	if resp.StatusCode >= 500 {
		return nil, errors.New("authorization transport failure")
	}
	return body, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
