# github Architecture

## Purpose

`uploader/internal/github` implements the authenticated GitHub REST calls used
for write-once publishing: repository identity lookup, Contents preflight,
create-only PUT, remote-hash inspection, and sanitized failure classification.
It never sends an update request, never sends a `sha` on a create, never
assumes conditional-create semantics, and never retains remote response text,
request URLs, headers, or tokens in an error value.

## Boundaries and dependencies

- Imports the standard library plus the uploader's `protocol` package for
  `TranscriptJob`, `RemoteFileKind`, and `ErrorCategory`.
- Credentials arrive through the narrow `CredentialSource` interface
  (`AuthorizationHeader`, `ForceRefresh`), which `auth.Manager` implements.
  This package never reads the Keychain and never stores a token.
- Consumers: `processor` depends on the `Publisher` interface
  (`InspectFile`, `Publish`); `auth.HTTPRepositoryVerifier` is a separate
  implementation of the connect sanity checks and does not use this client.
- Tests point `ClientConfig.BaseURL` at an `httptest` server; plain HTTP is
  accepted only for loopback hosts, so no test requires network access.

## Contracts and invariants

### Client configuration

```go
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

func NewClient(cfg ClientConfig) (*Client, error)
func (c *Client) Branch() string
```

Defaults and constants:

| Constant | Value |
| --- | --- |
| `DefaultBaseURL` | `https://api.github.com` |
| `DefaultRequestTimeout` | `30 * time.Second` |
| `DefaultMaxResponseBytes` | `1 << 20` (1 MiB) |
| API version header | `X-GitHub-Api-Version: 2026-03-10` |
| Accept header | `application/vnd.github+json` |
| User agent | `lecture-transcripts-uploader` |

`NewClient` rejects a missing/invalid base URL, a non-HTTPS base URL that is
not a loopback host, and empty owner/repo/branch. Every request uses the
configured timeout as a total context deadline; a timeout, cancellation, DNS,
or connection failure is classified retryable and the underlying error text is
discarded. A response body larger than `MaxResponseBytes` is permanent.

### Request behavior

- `do` sends one logical request; on HTTP 401 it calls
  `CredentialSource.ForceRefresh` once and retries once. If the refresh fails,
  it returns `CategoryAuth` with status 401. A second 401 is returned to the
  caller and classified by the operation's status mapping.
- `once` builds the target from `BaseURL` + a pre-escaped path, sets the fixed
  headers, obtains `AuthorizationHeader(ctx)` (classified `CategoryAuth` when
  it errors), and reads a bounded body.
- `opName` labels failures `contents.get`, `contents.put`, `repos.get`, or
  `request`; error strings contain only op, category, and HTTP status.
- `contentsPath` URL-escapes every segment; `repoPath` escapes owner and repo.
  The branch is only ever a query or JSON body value.

### Read operations

```go
func (c *Client) GetRepository(ctx context.Context) (Repository, error)
func (c *Client) InspectFile(ctx context.Context, path string) (RemoteFile, error)
```

`Repository` is the bounded subset `{ID, FullName, DefaultBranch}`.
`GetRepository` requires 200 and a positive ID plus a non-empty full name;
otherwise it classifies.

`InspectFile` performs the preflight GET with `?ref=<branch>`:

| Response | `RemoteFile.Kind` |
| --- | --- |
| 200 JSON object with `type:"file"` and a parseable hash | `file` (`RemoteFile`) |
| 200 JSON object with `type:"file"` but missing/malformed hash | `malformed` (`RemoteMalformed`) |
| 200 JSON array | `directory` (`RemoteDirectory`) |
| 200 `type:"dir"` | `directory` |
| 200 `type:"symlink"` | `symlink` |
| 200 `type:"submodule"` | `submodule` |
| 200 unknown `type` | `malformed` |
| 404 | `missing` (`RemoteMissing`), not an error |
| other | classified `*Error` |

`RemoteFile` carries `{Path, Kind, BlobSHA, ContentHash *string, Size}` and
never body text.

### Remote hash parsing

```go
func ParseTranscriptHash(content []byte) (string, bool)
```

- Empty content → false.
- CRLF is normalized to LF.
- If the first line is `---`, the top block is parsed (legacy form still
  accepted) up to the next `---`; otherwise the bottom block is found by
  trimming trailing blank lines and requiring the last line to be `---` with a
  preceding `---` line.
- Inside the block, only lines beginning `transcript_sha256:` are considered;
  the value is trimmed of whitespace and surrounding single/double quotes and
  must match `^[0-9a-f]{64}$`.
- A duplicated or missing hash line returns false, so an ambiguous remote file
  is never trusted for same-hash comparison.

### Create and publish

```go
type CreateResult struct { Path, BlobSHA, CommitSHA string }
func (c *Client) CreateFile(ctx context.Context, path string, content []byte, message string) (CreateResult, error)

type PublishOutcome string
const (
    OutcomeCreated   PublishOutcome = "created"
    OutcomeUnchanged PublishOutcome = "unchanged"
    OutcomeConflict  PublishOutcome = "conflict"
)
type PublishResult struct { Outcome PublishOutcome; Remote RemoteFile; HTTPStatus *int }
func (c *Client) Publish(ctx context.Context, path string, job protocol.TranscriptJob, content []byte) (PublishResult, error)
```

- `CreateFile` sends a PUT whose body is exactly
  `{message, content(base64), branch}`. It never includes `sha`, `author`, or
  `committer`. Only HTTP 201 with a `content` object carrying a non-empty `sha`
  is accepted; a 201 without that documented shape is `CategoryRetryable` so
  the caller re-inspects. Every other status is classified.
- `Publish` is preflight-plus-create:
  1. `InspectFile`; `missing` → `CreateFile`;
  2. on create error → `resolveCreateFailure`: a `CategoryAuth` error is
     returned directly (a fresh GET cannot change it); otherwise the path is
     re-GET and resolves to `unchanged` (same hash), `conflict` (different,
     malformed, or non-file), or the original classified error (still absent);
  3. an existing `file` whose parsed hash equals `job.ContentHash` →
     `OutcomeUnchanged` (HTTPStatus 200) without a write;
  4. any other existing kind or a differing hash → `OutcomeConflict`;
  5. a successful create → `OutcomeCreated` (HTTPStatus 201).
- The client never sends an update request and never deletes a remote file.

### Commit message

```go
func CommitMessage(job protocol.TranscriptJob, path string) string
// "Add %s %s %d (%s)" -> courseName, kind, lectureNumber, path
```

Examples: `Add EECS 491 lecture 6 (eecs491/006.md)` and
`Add EECS 491 discussion 1 (eecs491/discussions/001.md)`.
`TestCommitMessage` and `TestPublishCreatesWithoutSHA` assert the exact string.

### Error categories

```go
type Category string
const (
    CategoryRetryable  Category = "retryable"
    CategoryRateLimit  Category = "rate_limit"
    CategoryAuth       Category = "auth"
    CategoryPermission Category = "permission"
    CategoryPermanent  Category = "permanent"
)
func (c Category) Retryable() bool          // retryable || rate_limit
func (c Category) ProtocolCategory() protocol.ErrorCategory
```

`ProtocolCategory` maps `auth → reauthorization_required`,
`permission → rejected_permission`, everything else → `internal`.

`classifyStatus` uses only status, rate-limit headers, and a bounded prefix of
the documented `message` field:

| Status | Category |
| --- | --- |
| 401 | `auth` |
| 403 with rate limit signal, or message containing "bad credentials" | `rate_limit` or `auth` |
| other 403 | `permission` |
| 404 | `permission` (target-path 404 is intercepted by `InspectFile` first) |
| 409 | `permanent` |
| 429 | `rate_limit` |
| >= 500 | `retryable` |
| other | `permanent` |

Rate-limit detection: `x-ratelimit-remaining: 0` (trimmed), a non-empty
`Retry-After`, or a `message` containing "rate limit". `messageContains`
truncates the body to 4096 bytes before parsing, truncates the message to 120
characters, lowercases both sides, and never retains the message.

`Error` is `{Category, HTTPStatus, Op}` with an `Error()` string that contains
only op/category/status; it deliberately does not wrap the network error or
response body.

## Data flow

```
processor.processJob
  └─ markdown.RenderPlain / RenderTimestamped
  └─ Client.Publish(path, job, content)
       ├─ InspectFile (GET contents?ref=branch)
       │    ├─ missing -> CreateFile (PUT, no sha)
       │    │     ├─ 201 documented -> OutcomeCreated
       │    │     └─ error -> resolveCreateFailure -> re-GET -> unchanged|conflict|error
       │    ├─ file + same hash -> OutcomeUnchanged
       │    └─ file + different hash / directory / symlink / submodule / malformed
       │         -> OutcomeConflict
       └─ PublishResult -> processor persists uploaded/unchanged/permanent_conflict
```

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `client.go` | Bounded authenticated HTTP client, repository lookup, path escaping, one forced refresh after 401 | `DefaultBaseURL`, `DefaultRequestTimeout`, `DefaultMaxResponseBytes`, `CredentialSource`, `ClientConfig`, `Client`, `NewClient`, `Client.Branch`, `Repository`, `Client.GetRepository` |
| `contents.go` | Preflight, remote inspection/classification, hash parsing, create-only PUT, write-once publish | `RemoteFile`, `PublishOutcome`, `OutcomeCreated`, `OutcomeUnchanged`, `OutcomeConflict`, `PublishResult`, `CommitMessage`, `ParseTranscriptHash`, `Client.InspectFile`, `CreateResult`, `Client.CreateFile`, `Client.Publish` |
| `errors.go` | Closed category vocabulary, HTTP classification, sanitized `Error` | `Category`, `CategoryRetryable`, `CategoryRateLimit`, `CategoryAuth`, `CategoryPermission`, `CategoryPermanent`, `Category.Retryable`, `Category.ProtocolCategory`, `Error`, `Error.Retryable` |
| `contents_test.go` | Fake-server create/unchanged/conflict/race/auth/rate-limit/network tests | `TestPublishCreatesWithoutSHA`, `TestPublishUnchangedRevisit`, `TestPublishConflictOnDifferentHash`, `TestPublishConflictOnMalformedFile`, `TestPublishConflictOnDirectoryArray`, `TestPublishConflictOnSymlink`, `TestPublishRaceResolvesToUnchanged`, `TestPublishRaceResolvesToConflict`, `TestPublishRaceStillAbsentReturnsClassifiedError`, `TestRateLimitClassification`, `TestAuthFailureRefreshesOnce`, `TestAuthFailureWhenRefreshUnavailable`, `TestPermissionClassification`, `TestRepositoryNotFoundIsPermission`, `TestTransientServerErrorIsRetryable`, `TestNetworkFailureIsRetryable`, `TestTimeoutIsRetryable`, `TestOversizedResponseIsPermanent`, `TestNonCreatedUpdateResponseIsRejected`, `TestProtocolCategoryMapping`, `TestErrorStringIsSanitized`, `TestParseTranscriptHash`, `TestNewClientRejectsInsecureBaseURL`, `TestCommitMessage` |

## Testing and verification

Run `cd uploader && go test ./internal/github/ -count=1`. The suite uses an
`httptest` fake server that records method, path, query, headers, and body, and
a `fakeCredentials` source that counts header/refresh calls. It proves:

- create sends no `sha`, no `author`, no `committer`, the exact commit message,
  `branch=main`, the `ref` query on GET, and the exact Accept/API-version/
  Authorization headers (`TestPublishCreatesWithoutSHA`);
- same-hash revisit produces `unchanged` with no PUT
  (`TestPublishUnchangedRevisit`);
- differing, malformed, directory, and symlink content produce `conflict` with
  no overwrite;
- a create race resolves to `unchanged`, `conflict`, or a classified error on
  re-GET (`TestPublishRace*`);
- rate limiting, permission, auth-with-refresh, auth-without-refresh, 5xx,
  network, timeout, and oversized responses map to the right category;
- `Error.Error()` contains no secret and `ProtocolCategory` maps correctly.

`processor` additionally reads `uploader/testdata/existing-same-hash.md`,
`existing-different-hash.md`, and `malformed-lecture.md` through
`ParseTranscriptHash` in `TestWriteOnceFixtures`.

## Related plan sections

- `TECHNICAL_PLAN.md` → "Stage 6" items 3–6 (Contents API, commit message,
  path derivation, write-once classification, error categories).
- `TECHNICAL_PLAN.md` → "The GitHub Contents API is treated as
  preflight-plus-create" paragraph in "The Technical Plan".
- `TECHNICAL_PLAN.md` → "Normative implementation contracts → Canonical
  source-URL vectors" (publish omission is enforced by `markdown`, while the
  queue stores the canonical URL).
- `docs/SECURITY.md` → "Remote-hash trust", "Write-once conflict handling".
- `docs/TROUBLESHOOTING.md` → "Permanent conflicts".

## How to change this package safely

1. Never add a `sha` to a create PUT or add an update/delete method. Write-once
   behavior is a plan guardrail.
2. Never trust a 2xx other than the documented 201 create response; always
   re-inspect after an unexpected create response.
3. Keep `ParseTranscriptHash` strict: a duplicated, missing, or malformed hash
   must not be treated as same-hash.
4. Keep errors category-only. Do not wrap `*url.Error`, response bodies,
   request URLs, or headers.
5. Keep the fixed headers and the 30-second request timeout in sync with the
   plan; the timeout must stay below the 10-minute queue lease.
6. Update the fake-server tests for every new response branch; do not weaken
   `TestErrorStringIsSanitized`. No open questions remain: every claim above
   is implemented in `client.go`/`contents.go`/`errors.go` and asserted by
   `contents_test.go` or the processor fixtures.
