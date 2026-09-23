# auth Architecture

## Purpose

`uploader/internal/auth` owns the GitHub App credential and the GitHub App
Device Authorization Flow. It is the only package allowed to touch credential
storage. Every other package receives at most an in-memory
`Authorization: Bearer <token>` header value; the extension and the queue never
see a token. The package also performs the connect-time repository sanity
checks before authorization is reported as `connected`.

## Boundaries and dependencies

- Imports `context`, `encoding/json`, `errors`, `io`, `net/http`, `net/url`,
  `strconv`, `strings`, `sync`, `time`, and the uploader's `protocol` package
  for the closed auth-state vocabulary.
- The Darwin Keychain adapter uses cgo and `Security.framework`
  (`#cgo LDFLAGS: -framework Security -framework CoreFoundation`). There is no
  plaintext fallback: `keychain_unsupported.go` is built for
  `!darwin || !cgo` and `NewStore()` returns `ErrUnsupportedPlatform`.
- Consumers: `processor` depends only on its narrow `AuthManager` interface
  (`State`, `Challenge`, `Begin`, `Poll`, `Reset`); `github.Client` depends only
  on `CredentialSource` (`AuthorizationHeader`, `ForceRefresh`). `cmd` wires the
  concrete `*auth.Manager`.
- The package never logs or returns token material; errors are sentinel values
  with fixed strings.

## Contracts and invariants

### Keychain records

| Item | Value |
| --- | --- |
| Keychain service | `com.neelbangera.lecturetranscripts` (`KeychainService`) |
| Credential account | `github-app-user-token` (`CredentialAccount`) |
| Device-flow account | `github-app-device-transaction` (`TransactionAccount`) |
| Item class | generic password (`kSecClassGenericPassword`) |
| Accessibility | `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly` |

The credential is one replaceable record, so a refresh can never leave a
partial token pair. The device transaction is always a separate record. The
Darwin adapter writes with `SecItemUpdate` and falls back to `SecItemAdd` only
on `errSecItemNotFound`; delete treats `errSecItemNotFound` as success. An
empty access token or empty device code is refused before writing.

### Types

```go
type Credential struct {
    AccessToken           string    `json:"accessToken"`
    RefreshToken          string    `json:"refreshToken,omitempty"`
    AccessTokenExpiresAt  time.Time `json:"accessTokenExpiresAt,omitempty"`
    RefreshTokenExpiresAt time.Time `json:"refreshTokenExpiresAt,omitempty"`
    TokenType             string    `json:"tokenType,omitempty"`
    RepositoryID          int64     `json:"repositoryId"`
    RepositoryFullName    string    `json:"repositoryFullName"`
}

type DeviceTransaction struct {
    DeviceCode              string        `json:"deviceCode"`
    UserCode                string        `json:"userCode"`
    VerificationURI         string        `json:"verificationUri"`
    VerificationURIComplete string        `json:"verificationUriComplete,omitempty"`
    ExpiresAt               time.Time     `json:"expiresAt"`
    Interval                time.Duration `json:"interval"`
}

type Challenge struct { // user-facing subset; no token material
    UserCode, VerificationURI, VerificationURIComplete string
    ExpiresAt time.Time
    Interval  time.Duration
}
```

### Interfaces

```go
type Store interface {
    LoadCredential() (*Credential, error)
    SaveCredential(Credential) error
    DeleteCredential() error
    LoadTransaction() (*DeviceTransaction, error)
    SaveTransaction(DeviceTransaction) error
    DeleteTransaction() error
}

type RepositoryVerifier interface {
    VerifyRepository(ctx context.Context, credential Credential) error
}

type State = protocol.AuthState
```

### Manager

```go
func NewManager(store Store, cfg Config) (*Manager, error)
func (m *Manager) State() protocol.AuthState
func (m *Manager) Challenge() *Challenge
func (m *Manager) Begin(ctx context.Context) (*Challenge, error)
func (m *Manager) Poll(ctx context.Context) (protocol.AuthState, error)
func (m *Manager) Connect(ctx context.Context) (protocol.AuthState, error)
func (m *Manager) AuthorizationHeader(ctx context.Context) (string, error)
func (m *Manager) ForceRefresh(ctx context.Context) (string, error)
func (m *Manager) Reset() error
```

`Config` fields: `ClientID`, `RepositoryID`, `Owner`, `Repo`, `Branch`
(required, from `config.Config`), plus test seams `DeviceCodeURL`, `TokenURL`,
`APIBaseURL`, `HTTPClient`, `Now`, `Sleep`, `RefreshThreshold`, `Verifier`.
Defaults: device code `https://github.com/login/device/code`, token
`https://github.com/login/oauth/access_token`, API `https://api.github.com`,
refresh threshold 5 minutes, poll interval 5 seconds, request timeout 30
seconds, response cap 64 KiB. `NewManager` validates required fields, restores
persisted state, and performs no network access.

### Device flow behavior

- `Begin` first tries an existing credential: it obtains an authorization
  header (refreshing near expiry) and calls `VerifyRepository`. Success sets
  `connected` and returns `(nil, nil)`. `ErrTargetRepositoryUnavailable`
  clears the credential and sets `target_repository_unavailable`;
  `ErrReauthorizationRequired` clears the credential and falls through to the
  device flow; other errors set `not_connected` and are returned.
- A persisted, unexpired transaction resumes: `Begin` returns the same
  challenge without a new device-code request. An expired transaction is
  deleted so the next call starts a new flow.
- Otherwise `requestDeviceCode` POSTs `client_id` to the device-code endpoint
  and persists a `DeviceTransaction` before any polling. `expires_in` and
  `interval` are taken from the server; `interval <= 0` falls back to 5
  seconds. `verification_uri_complete` is used when GitHub supplies it.
- `Poll` performs exactly one token poll. It POSTs `client_id`, `device_code`,
  `grant_type=urn:ietf:params:oauth:grant-type:device_code`, and the configured
  numeric `repository_id`. Handling of `error`:
  - `authorization_pending` → stay `authorizing`;
  - `slow_down` → new interval = `transaction.Interval + 5s` unless the
    response carries `interval`, then that value; the updated transaction is
    persisted and the challenge refreshed;
  - `installation_missing_access` → discard the transaction, set
    `target_repository_unavailable`, return `ErrTargetRepositoryUnavailable`;
  - any other terminal error (`access_denied`, `expired_token`,
    `device_flow_disabled`, ...) → discard the transaction, set
    `reauthorization_required`, return `ErrReauthorizationRequired`.
  Transport failures and unparseable bodies keep the transaction and return
  `authorizing` so the next poll can recover. At or past the server-provided
  `ExpiresAt`, `Poll` deletes the transaction and surfaces
  `reauthorization_required` without a token call.
- On success the credential is built from `access_token`, optional
  `refresh_token`, `token_type`, `expires_in`, `refresh_token_expires_in`, and
  the configured repository ID/full name; it is saved, the transaction is
  deleted, and `VerifyRepository` runs before `connected` is reported.
  Verification failures clear the just-authorized credential and map as above.
- `Connect` drives `Begin` + `Poll` until a terminal state, sleeping the
  current challenge interval between polls (injectable for tests).
- `completeVerificationURI` returns the server value when present; otherwise,
  when the server URI is `https://github.com/...`, it synthesizes
  `<verification_uri>?user_code=<userCode>` so the popup can offer one-click
  authorization. It never fabricates a URI for a non-GitHub host.

### Refresh threshold and header rules

- `DefaultRefreshThreshold = 5 * time.Minute`. A token is refreshed when it has
  five minutes or less remaining, or when no usable expiry is recorded, and a
  refresh token exists.
- `ForceRefresh` refreshes exactly once (used by the GitHub client after a 401).
- A refresh with no refresh token, a rejected refresh (`invalid_grant`, empty
  access token), or an unreadable response sets `reauthorization_required` and
  returns `ErrReauthorizationRequired`. A 401 using the newly refreshed token
  becomes `reauthorization_required` in the GitHub client, not an infinite
  retry.
- `refresh` writes the entire updated credential in one `SaveCredential`; a
  failed save leaves the previous in-memory credential untouched.
- `AuthorizationHeader` returns `"Bearer " + AccessToken`; when no credential
  exists it sets `not_connected` and returns `ErrNotConnected`.

### Reset

`Reset` deletes both Keychain records, clears the in-memory credential,
transaction, and challenge, and sets `not_connected`. Queue jobs are untouched.
Failures from either delete are joined and returned.

### Connect sanity checks (`HTTPRepositoryVerifier`)

```go
type HTTPRepositoryVerifier struct {
    BaseURL, Owner, Repo, Branch string
    RepositoryID                 int64
    HTTPClient                   *http.Client
}
```

It sends `Accept: application/vnd.github+json`,
`X-GitHub-Api-Version: 2026-03-10`, `User-Agent:
lecture-transcripts-uploader`, and `Authorization: Bearer <token>` to:

1. `GET /repos/{owner}/{repo}` — requires HTTP 200, the exact numeric
   `repositoryId`, and `full_name` equal (case-insensitive) to `owner/repo`;
2. `GET /repos/{owner}/{repo}/contents?ref={branch}` — requires HTTP 200 to
   prove the branch is initialized and Contents permission is usable.

Mapping: 200 → pass/continue; 401 → `ErrReauthorizationRequired`; 403/404 →
`ErrTargetRepositoryUnavailable`; anything else or a malformed body →
`errRepositoryCheck`. The branch is always a query value, never a path.

### Sentinels

`ErrNoCredential`, `ErrNoTransaction`, `ErrUnsupportedPlatform`,
`ErrNotConnected`, `ErrReauthorizationRequired`,
`ErrTargetRepositoryUnavailable`.

## Data flow

```
popup connect
  └─ processor.handleConnect → auth.Manager.Begin
       ├─ credential exists → AuthorizationHeader → VerifyRepository
       │     └─ connected | target_repository_unavailable | device flow
       ├─ unexpired transaction → challenge (authorizing)
       └─ device-code POST → SaveTransaction → challenge (authorizing)
  └─ drain loop → auth.Manager.Poll (one token poll per pass)
       ├─ pending/slow_down/transport → authorizing (transaction persisted)
       ├─ installation_missing_access → target_repository_unavailable
       ├─ terminal/expired → reauthorization_required (transaction deleted)
       └─ token → SaveCredential → VerifyRepository → connected

GitHub request path
  └─ github.Client → CredentialSource.AuthorizationHeader
       └─ near expiry → refresh (atomic SaveCredential) → Bearer header
  └─ 401 → ForceRefresh once → retry once → otherwise CategoryAuth
```

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `auth.go` | Keychain identifiers, sentinels, credential/transaction/challenge types, `Store`/`RepositoryVerifier` contracts | `KeychainService`, `CredentialAccount`, `TransactionAccount`, `ErrNoCredential`, `ErrNoTransaction`, `ErrUnsupportedPlatform`, `ErrNotConnected`, `ErrReauthorizationRequired`, `ErrTargetRepositoryUnavailable`, `Credential`, `DeviceTransaction`, `Challenge`, `Store`, `RepositoryVerifier`, `State` |
| `keychain_darwin.go` | cgo Security.framework generic-password adapter (build `darwin && cgo`) | `NewStore` |
| `keychain_unsupported.go` | Fail-closed stub (build `!darwin || !cgo`) | `NewStore` |
| `web_flow.go` | Device flow, polling, refresh, reset, repository verifier | `DefaultDeviceCodeURL`, `DefaultTokenURL`, `DefaultAPIBaseURL`, `DefaultRefreshThreshold`, `DefaultPollInterval`, `Config`, `Manager`, `NewManager`, `Manager.State`, `Manager.Challenge`, `Manager.Begin`, `Manager.Poll`, `Manager.Connect`, `Manager.AuthorizationHeader`, `Manager.ForceRefresh`, `Manager.Reset`, `HTTPRepositoryVerifier`, `HTTPRepositoryVerifier.VerifyRepository` |
| `auth_test.go` | Fake store/provider/verifier tests | `TestBeginPersistsTransaction`, `TestBeginSynthesizesCompleteVerificationURI`, `TestPollPendingSlowDownThenSuccess`, `TestPollTerminalErrorDeletesTransaction`, `TestPollInstallationMissingMapsToTargetUnavailable`, `TestPollStopsAtServerExpiry`, `TestBeginResumesPersistedTransaction`, `TestBeginReplacesExpiredTransaction`, `TestConnectRunsToCompletion`, `TestSanityFailureClearsJustAuthorizedCredential`, `TestHTTPRepositoryVerifier`, `TestHTTPRepositoryVerifierUsesBranchQueryAndHeaders`, `TestAuthorizationHeaderRefreshesNearExpiry`, `TestAuthorizationHeaderUsesValidTokenWithoutRefresh`, `TestAuthorizationHeaderExpiringWithoutRefreshToken`, `TestAuthorizationHeaderNotConnected`, `TestForceRefreshRejectsInvalidGrant`, `TestRefreshFailureLeavesCredentialUntouched`, `TestResetDeletesBothRecords`, `TestRestoreStateFromStore`, `TestPollTransientTransportKeepsTransaction`, `TestBeginRevalidatesExistingCredential`, `TestNewManagerValidation`, `TestDeviceCodeTerminalErrorSurfacesReauthorization` |
| `keychain_unsupported_test.go` | Asserts the non-Darwin build fails closed (build-tagged `!darwin || !cgo`) | `TestNewStoreIsUnsupported` |

## Testing and verification

Run `cd uploader && go test ./internal/auth/ -count=1`. No test touches the
real Keychain or network; fakes cover the store, verifier, HTTP server, and
clock (`newFakeClock`, `fakeStore`, `fakeVerifier`, `authServer`). Coverage
includes transaction persistence before polling, resume/replace semantics,
`slow_down` interval persistence, terminal error cleanup, expiry without a
token call, synthesized complete verification URI, credential refresh and
atomic save, forced refresh rejection, reset of both records, restore-state
mapping, transient transport retention, repository sanity mapping, and that
terminal errors never leak `device-secret`/`ghu_` material.

## Related plan sections

- `TECHNICAL_PLAN.md` → "Stage 0" item 9 (selected device flow, Keychain
  records, refresh behavior) and item 10 (repository verification).
- `TECHNICAL_PLAN.md` → "Stage 5" (device flow, credential interface, Keychain
  adapter, connect/reset wiring).
- `TECHNICAL_PLAN.md` → "Canonical Native Messaging contract" (auth states,
  `authorization` fields, `reauthorization_required` /
  `target_repository_unavailable` behavior).
- `docs/SECURITY.md` → "Keychain records and device-flow storage", "No GitHub
  credentials in the extension", "Reset semantics".
- `docs/TROUBLESHOOTING.md` → "Authorization failures", "Target repository is
  unavailable".

## How to change this package safely

1. Never add a plaintext or file-based credential fallback. The unsupported
   build must keep failing closed.
2. Keep the credential as one atomic record and the device transaction as a
   separate account. A refresh must never write a partial pair.
3. Keep polling server-driven: honor `interval`, `slow_down`, and the
   server-provided expiry; never hardcode a flow duration.
4. Keep `installation_missing_access` mapped to
   `target_repository_unavailable`, not an endless reauthorization loop.
5. Keep the two connect sanity requests in order (repository identity, then
   Contents read) and keep the numeric-ID/full-name comparison strict.
6. Keep `repository_id` on the token request; it is configuration, not a value
   returned by the device-code response.
7. Do not let token material enter error strings, logs, status messages, or
   the `Challenge` type. Extend `auth_test.go` whenever a new branch is added.
   No open questions remain: the refresh threshold, polling behavior, storage
   layout, and error mapping are all directly verifiable in `web_flow.go` and
   the test suite.
