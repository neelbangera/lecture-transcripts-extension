# Security

This document describes the implemented security boundary: what each process is
allowed to see and do, where secrets live, and what the system does when data is
missing, conflicting, or rejected. `TECHNICAL_PLAN.md` remains the contract
authority; this is the owner-facing explanation of it.

## Extension permissions and host allowlist

`extension/manifest.json` requests only:

| Permission | Why it exists |
| --- | --- |
| `alarms` | One-minute `lecture-transcripts-drain` alarm that reconnects the Native Messaging port after service-worker suspension. |
| `nativeMessaging` | The only channel to the local uploader. |
| `storage` | Bounded pending-handoff outbox and metadata-only overflow notices in `chrome.storage.local`. |

The only host permission and content-script match is
`https://leccap.engin.umich.edu/*`. The extension has no GitHub host permission,
no `tabs`, `cookies`, `webRequest`, or `<all_urls>` permission, and no remote
code or server endpoint.

The Native Messaging host manifest allows exactly one origin:
`chrome-extension://<loaded-extension-id>/`. The host validates the
Chrome-supplied origin against that exact string before reading a frame; a
wildcard, a missing configuration, an alternate scheme, or a different ID all
fail closed. The rendered manifest is written with mode `0600`, and the
installer refuses to write it when the extension ID is missing or malformed.

## No GitHub credentials in the extension

The extension never contains or receives a GitHub access token, refresh token,
client secret, or App private key. It sees only closed auth states
(`not_connected`, `authorizing`, `connected`, `reauthorization_required`,
`target_repository_unavailable`, `protocol_mismatch`) plus the device-flow
`user_code` and GitHub verification URIs needed to finish authorization. The
uploader owns polling, token exchange, refresh, and all GitHub requests.

The job payload carries only the extracted transcript, the timestamped
transcript, selected lecture metadata, and a canonicalized source URL. Cookies,
authorization headers, raw HTML, and unrelated page text never cross the
browser-to-uploader boundary.

## Keychain records and device-flow storage

The uploader stores credentials in the macOS Keychain through cgo and
`Security.framework`. There is no plaintext fallback: a build that cannot reach
the Keychain fails with `credential storage requires macOS with cgo enabled`.

| Item | Value |
| --- | --- |
| Keychain service | `com.neelbangera.lecturetranscripts` |
| Credential account | `github-app-user-token` |
| Device-flow account | `github-app-device-transaction` |
| Item class | generic password |
| Accessibility | `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly` |

The credential record contains the access token, the refresh token when
provided, expiry timestamps, the granted numeric repository ID, and the
repository full name. It is written as one replaceable record, so a refresh can
never leave a partial token pair; a failed save leaves the previous record
untouched. The in-flight device transaction is a separate record containing the
device code, user code, verification URIs, server-provided expiry, and poll
interval. It is persisted before polling, resumes after a host restart while
unexpired, and is deleted on success, expiry, or a terminal device-flow error.

Before each GitHub request the uploader refreshes when the access token has five
minutes or less remaining, or when no usable expiry is recorded. A refresh
replaces the whole credential record atomically. A rejected refresh, a revoked
credential, or a 401 after a forced refresh surfaces `reauthorization_required`
and pauses uploads; it never retries indefinitely with a dead token.

## Local queue data and file modes

The uploader creates its data directory as `0700` and restricts every file it
owns:

| Path | Mode |
| --- | --- |
| `~/Library/Application Support/LectureTranscripts/` | `0700` |
| `~/Library/Application Support/LectureTranscripts/queue.sqlite3` | `0600` |
| `.../queue.sqlite3-wal`, `.../queue.sqlite3-shm` | `0600` |
| `.../queue.lock` | `0600` |
| `.../config.json` | owner-created, never committed |

Single-instance ownership is enforced by opening `queue.lock` with
`O_RDWR|O_CREAT` and acquiring an advisory `flock LOCK_EX|LOCK_NB` held for the
process lifetime. A second uploader process exits with `already_running` rather
than sharing the database.

Queue limits and retention:

- at most 500 jobs and 100 MiB of stored job JSON;
- after each uploaded or unchanged job, the oldest `uploaded`/`unchanged` rows
  older than 7 days are deleted until the queue is under both limits;
- `queued`, `uploading`, `retryable_error`, `permanent_conflict`, and every
  `rejected_*` row are never auto-deleted;
- a new job that would exceed either limit is rejected as
  `rejected_queue_full` without dropping an existing job.

The queue stores the canonical job JSON plus status metadata. It never stores a
token, and the database is never committed.

## Log redaction and rotation

Logs are JSON lines written to `~/Library/Logs/LectureTranscripts/uploader.log`
with the directory at `0700` and the file at `0600`. Rotation happens at 5 MiB
and keeps at most three files (`uploader.log`, `uploader.log.1`,
`uploader.log.2`); older files are deleted.

Only an allowlisted set of fields is written: timestamp, level, event,
`lectureKey`, `courseSlug`, `term`, `lectureNumber`, `status`, `errorCategory`,
and a sanitized source string. Anything that does not match the closed status
or error vocabulary is replaced with `[redacted]`. Known sensitive markers
(`transcript_body`, `token=`, `access_token`, `refresh_token`, `sessionid`,
`cookie`, `authorization`, `bearer `, `device_code`, `user_code`, and similar)
cause the containing value to be redacted. Transcript text, device codes,
tokens, cookies, and request bodies never appear in a log line.

The source field is reduced to `host + path` with the query and fragment
dropped, and is redacted entirely when it contains a sensitive marker. GitHub
failures are persisted as `{category, httpStatus}` only; the client never wraps
or logs a token, request URL query, or response body.

## Source-URL sanitization

Source URLs are canonicalized in two places: the extension canonicalizes before
building the job, and the uploader authoritatively re-canonicalizes before
publishing. Shared vectors in `protocol/source-url-vectors.json` define the
expected result.

Rules enforced by the code:

- only `https` (the publish renderer additionally requires the exact host
  `leccap.engin.umich.edu`);
- query and fragment are removed; they never reach published metadata or
  logs;
- userinfo, opaque URLs, non-default ports, and overlength values are rejected;
- path segments matching `token`, `session`, `auth`, or `sid` cause the source
  URL to be omitted from the published metadata rather than leaked;
- a rejected URL fails the job as `rejected_unsafe_url` before any metadata or
  overview lookup.

## Remote-hash trust

The remote `transcript_sha256` field in the bottom metadata block is the
authority for write-once comparison. The uploader parses it strictly: the block
must be delimited by `---` lines (bottom block for current files, legacy top
block still accepted), contain exactly one unquoted or quoted 64-character
lowercase hex `transcript_sha256` scalar. A missing, duplicated, malformed, or
non-base64 response is classified as a malformed remote file and becomes a
conflict; it is never trusted for a same-hash comparison.

Accepted Phase 1 invariant: a manual edit to the file body that leaves
`transcript_sha256` unchanged is classified as unchanged. The field is the
contract, not a re-hash of the remote body.

## Write-once conflict handling

Publishing is preflight plus create-only:

1. `GET` the target path on the configured branch.
2. If the path is absent, issue a create-only `PUT` without a `sha`.
3. Accept only a `201` response with the documented content object.
4. On any unexpected create response, re-`GET` the path and classify the result
   as unchanged, conflict, or the original classified error.

The uploader never sends an update request, never deletes a remote file, and
never assumes conditional-create semantics. A same-hash remote file is
`unchanged`. A different hash, a directory, a symlink, a submodule, or a
malformed file is `permanent_conflict` and the remote content is left untouched.
Only the configured machine-owned lecture paths
(`<courseSlug>/lectures/<NNN>.md`, `<courseSlug>/timestamped/<NNN>.md`, and the
plain-only `<courseSlug>/<NNN>.md`) are ever written.

## Reset semantics

There are two different resets; they are not interchangeable.

| Action | Effect |
| --- | --- |
| Popup **Reset connection** | Deletes the Keychain credential and in-flight device transaction and clears connection state. Queued jobs remain durable and resume after reconnection. |
| `scripts/uninstall-native-host.sh --reset` | Prints what it will remove, then removes the rendered host manifest, the built uploader, the queue database plus sidecars and lock, the machine-local config, and this host's Keychain records. It never touches the remote repository or the logs. |
| `scripts/uninstall-native-host.sh` (no flag) | Removes only the rendered host manifest and the built uploader. |

Discarding a job from the popup is a local queue operation for terminal
rejected or permanent-conflict rows; it never calls a GitHub delete. Retrying a
permanent conflict requires the owner to confirm the remote file was removed
first, and the uploader performs a fresh preflight `GET` before retrying.

## Private versus public repository

The destination repository may start private and be made public later; the job
format and logs must be safe in either mode. Practical consequences:

- The published file contains course, term, lecture number, date, capture time,
  the framed content hash, the two transcript sections, and a source URL only
  when it survives sanitization. Review whether a source URL is appropriate
  before making the repository public.
- Logs are sanitized regardless of repository visibility, but they still live
  only on this Mac and are never committed.
- The extension's transcript and metadata travel only over Native Messaging to
  the local uploader; making the repository public does not expose them to any
  additional party.
- The GitHub App remains installed only on this one repository with only
  Contents read/write. If the repository is shared or transferred, review the
  App installation and this Mac's Keychain records.

## Reporting and rotation of secrets

If a credential is suspected to be exposed, use the popup **Reset connection**
to delete the Keychain records, then revoke the App authorization from the
GitHub App settings and reconnect. If the machine-local config is suspected to
be copied, rotate the App's client ID by creating a replacement App and updating
`config.json`; no client secret exists to rotate. Never paste a token, device
code, real extension ID, or raw authenticated capture into an issue, a prompt,
or a committed file.
