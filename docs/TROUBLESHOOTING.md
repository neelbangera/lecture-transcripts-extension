# Troubleshooting

Start with the log and the popup:

- Popup: `chrome://extensions` → **Lecture Transcripts** → **Errors** for the
  service worker, and the extension popup for connection, queue, pending, and
  overflow state.
- Uploader log: `~/Library/Logs/LectureTranscripts/uploader.log` (plus
  `uploader.log.1` and `uploader.log.2`). It is JSON lines with mode `0600`.
- Queue database: `~/Library/Application Support/LectureTranscripts/queue.sqlite3`
  (`0600`).
- Host manifest: `~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json`.

Status names below are the exact contract values; the popup shows a plain-text
label for each. See `TECHNICAL_PLAN.md` for the closed vocabularies and
[SECURITY.md](SECURITY.md) for what may appear in logs.

## Local uploader unavailable (host not found)

Symptoms: popup shows **Local uploader is unavailable**; the service worker
reports a missing native messaging host; `host_unavailable` appears in the
popup or log.

Causes and fixes, in order of likelihood:

1. The host manifest is not installed. Run
   `scripts/install-native-host.sh <loaded-extension-id>`.
2. The extension ID changed because the unpacked path moved. Rerun the
   installer with the new ID and restart Chrome (see
   [Extension-ID drift](#extension-id-drift)).
3. The binary path in the manifest is missing or not executable. Rebuild with
   `scripts/build-uploader.sh` and reinstall the host.
4. Chrome has not reread the registration. Fully quit Chrome (Cmd-Q) and
   reopen it.
5. The manifest is invalid JSON or still contains a `{{...}}` placeholder. The
   installer refuses to write those; if a file was edited by hand, rerun the
   installer to regenerate it.

The extension keeps unacknowledged handoffs in its bounded outbox and retries
on the one-minute drain alarm, so a temporarily missing host does not lose a
captured job.

## Protocol mismatch

Symptom: popup shows **Extension and uploader versions are incompatible**;
`authState` or the error category is `protocol_mismatch`; the uploader log
contains `protocol_error`.

The protocol is versioned and fails closed. This means the extension bundle and
the uploader binary were built from different protocol versions. Fix by
rebuilding and reinstalling both from the same checkout:

```sh
npm run build
scripts/build-uploader.sh
scripts/install-native-host.sh <loaded-extension-id>
```

Then reload the unpacked extension and restart Chrome. The popup footer shows
both build versions; they should match `package.json`.

## Authorization failures

Symptom: popup shows **GitHub is not connected** (`not_connected`), **Finish
GitHub authorization** (`authorizing`), **Reconnect GitHub**
(`reauthorization_required`), or the device flow expires without completing.

- `not_connected`: no credential is stored. Choose **Connect GitHub** and
  finish the device flow at the GitHub verification link.
- `authorizing` that never completes: the device code may have expired, or the
  authorization was denied. The uploader deletes an expired transaction and the
  next connect starts a new flow. If GitHub reports `device_flow_disabled`, the
  App was created without Device Flow; enable it in the App settings.
- `reauthorization_required`: the token was revoked, expired without a usable
  refresh token, or a refresh was rejected. Choose **Reset connection**, then
  **Connect GitHub** again. The queue is untouched and resumes after reconnect.
- macOS Keychain prompt denied: the uploader has no plaintext fallback and
  reports a storage failure. Choose **Always Allow** on the next attempt. A
  rebuilt binary can legitimately prompt again.
- Authorizing a GitHub account that cannot access the target repository clears
  the new credential and reports `target_repository_unavailable` (next
  section).

## Target repository is unavailable

Symptom: popup shows **Target repository is unavailable**
(`target_repository_unavailable`); connect fails after a successful device
authorization.

The uploader verifies `GET /repos/{owner}/{repo}` (numeric ID and full name)
and `GET /repos/{owner}/{repo}/contents?ref=main` before reporting connected.
Check each precondition:

1. `config.json` has the exact numeric `repositoryId` for
   `neelbangera/lecture-transcripts` and `owner`/`repo`/`branch` are exactly
   `neelbangera`, `lecture-transcripts`, `main`.
2. The GitHub App is installed on that repository only.
3. The App's repository permission **Contents** is **Read and write**.
4. `main` exists and has at least one commit. The uploader never initializes an
   empty repository.
5. The account that authorized has access to the repository (or the App
   installation grants it).

A 403 or 404 on either sanity request is `target_repository_unavailable`; a 401
is `reauthorization_required`. Fix the provisioning, then reconnect. The
machine-local file shape is documented in [SETUP.md](SETUP.md).

## Pending handoffs

Symptom: popup shows a nonzero **Pending handoffs** count, or
`rejected_handoff_full` / **Rejected — pending handoff is full**.

The extension keeps a bounded three-job outbox only until the uploader
acknowledges durable persistence. A pending entry means the handoff was not yet
acknowledged (uploader unavailable, service worker suspended, or a rejected
response). It is replayed on the next alarm or popup refresh. When the outbox is
full, the new handoff is rejected visibly as `rejected_handoff_full` and only a
bounded metadata-only notice is kept; the system never drops an older pending
transcript and never claims a full-outbox rejection was saved.

To recover: make sure the local uploader is reachable, wait for the drain alarm
or press **Refresh**, and reopen the affected transcript after capacity is
available. **Reopen to recapture** in the popup lists captures that were not
durably accepted; their full transcript is not stored in the notice.

## Queue-full notices

Symptom: popup shows **Rejected — uploader queue is full**
(`rejected_queue_full`).

The durable queue holds at most 500 jobs or 100 MiB of job JSON. A new job that
would exceed either limit is rejected without dropping an existing job. Uploaded
and unchanged rows older than 7 days are pruned automatically after a successful
job, but `queued`, `uploading`, `retryable_error`, `permanent_conflict`, and
`rejected_*` rows are never auto-deleted.

To recover: let due jobs upload, discard terminal rejected or conflict rows from
the popup, or retry resolved ones. Then reopen the transcript to recapture. A
`rejected_queue_full` handoff is not stored; the popup keeps only the
metadata-only overflow notice.

## Alarm-delayed retries

Symptom: a `retryable_error` job does not retry immediately; popup shows
**Temporary error; will retry** and a `nextAttemptAt`.

Retries use the fixed schedule `5s, 30s, 2m, 10m, 1h` with ±20% jitter, capped
at 1h after the fifth attempt. The attempt count is stored in the queue row, so
it survives restarts. The Manifest V3 service worker may be suspended; the
one-minute `lecture-transcripts-drain` alarm reconnects the port and lets the
uploader resume due work. Backoff values are exact while the host is alive;
after suspension they are lower bounds and may wait for the next alarm.

Chrome being completely closed is out of scope: the queue resumes the next time
the native host connects. Only `retryable` GitHub failures auto-retry
(timeouts, network failures, 429, 5xx, and rate-limited 403s). A `401` is an
authorization problem, and a `permission` failure is terminal.

## Log location and reading

```sh
ls -la "$HOME/Library/Logs/LectureTranscripts"
tail -n 20 "$HOME/Library/Logs/LectureTranscripts/uploader.log"
```

Logs rotate at 5 MiB and keep at most three files. Each line is JSON with
`time`, `level`, `event`, and optional `lectureKey`, `courseSlug`, `term`,
`lectureNumber`, `status`, `errorCategory`, and a sanitized `source`. Events
include `startup`, `config_loaded`, `lock_already_running`, `queue_opened`,
`job_enqueued`, `job_duplicate`, `job_queue_full`, `job_uploaded`,
`job_unchanged`, `job_retryable_error`, `job_permanent_conflict`,
`job_rejected`, `auth_state`, `github_request`, and `protocol_error`.

Transcript text, tokens, cookies, device codes, and query strings are never
logged; anything outside the closed status vocabulary is written as
`[redacted]`. If a log line shows `lock_already_running`, another uploader
process holds `queue.lock`; close the other instance rather than deleting the
lock file.

## Permanent conflicts

Symptom: popup shows **Conflict — manual review needed**
(`permanent_conflict`); the job records `lastErrorCategory`, the target path,
the local `contentHash`, and the remote `transcript_sha256` or a
`remoteFileKind` such as `malformed`, `directory`, `symlink`, or `submodule`.

A conflict means the target path already exists with different, malformed, or
non-file content. The uploader never overwrites or deletes it. To resolve:

1. Inspect the remote file at the recorded path in GitHub.
2. Back it up if it matters, then delete it in the GitHub UI (or rename it out
   of the machine-owned path).
3. In the popup, choose **Retry** and confirm the remote file was removed. The
   uploader performs a fresh preflight `GET` before retrying; it does not bypass
   write-once checks.
4. If the conflict is not worth resolving, choose **Discard** to delete the
   local queue row. Discard is local only and never calls a GitHub delete.

A same-hash remote file is never a conflict: it is reported as
**Already uploaded; unchanged**. A body-only manual edit that leaves
`transcript_sha256` unchanged is intentionally classified as unchanged.

## Oversized or rejected metadata

These are terminal local rejections; the popup shows the matching
**Rejected — ...** label and the job is not retried. The transcript text may
still be present on the page, so reopen and reactivate after addressing the
cause.

| Status | Meaning | Recovery |
| --- | --- | --- |
| `rejected_oversized` | The transcript or serialized job exceeds the byte caps (`460800` bytes per transcript field, `972800` bytes serialized job, `1048576` bytes per Native Messaging frame). | Confirm the page rendered a complete, non-duplicated transcript. The observed supported samples are far below the caps; a genuine overflow means the page shape changed and the plan must be revisited, not the cap. |
| `rejected_missing_identity` | Required course, term, lecture number, or date was absent. | Confirm the page is a supported lecture page and the transcript is open. Missing metadata fails closed; never guess a path. |
| `rejected_ambiguous_metadata` | A title or date was ambiguous (for example a title with no numeric lecture prefix, a literal Spring/Summer label, or an invalid calendar date). | Only supported course/term/page shapes can be captured; the category badge is never a lecture number. Add a verified observation before expecting this page to work. |
| `rejected_invalid_hash` | The framed transcript hash was missing or malformed. | Reopen the transcript and reactivate so the extension recomputes the hash. |
| `rejected_unsafe_url` | The source URL failed canonicalization or exposed sensitive path segments. | Usually a session-specific URL; the transcript may still be captured from the canonical lecture page. |
| `rejected_unknown_field`, `rejected_invalid_schema` | The extension sent a payload that is not in the closed protocol schema. | This indicates a version mismatch; rebuild both sides from the same checkout. |
| `rejected_permission` | GitHub returned 403/404 for the repository or path after authorization. | Fix the App installation/Contents permission or repository state, then choose **Retry**. |

## Extension-ID drift

Symptom: capture stops reaching the uploader after the extension was moved,
reloaded from a different directory, or re-added in `chrome://extensions`; the
popup reports the uploader is unavailable, and the host manifest still lists an
old `chrome-extension://...` origin.

An unpacked extension's ID is derived from its directory path, so the ID is only
stable while the path is unchanged. Fix:

1. Copy the current 32-character ID from `chrome://extensions`.
2. Run `scripts/install-native-host.sh <new-loaded-extension-id>`.
3. Fully quit and reopen Chrome.
4. Confirm the manifest contains exactly one origin and the new ID:

```sh
grep -o 'chrome-extension://[a-p]*/' "$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json"
```

Never hand-edit the rendered manifest, and never add a wildcard origin. The
extension ID is deliberately not committed anywhere in this repository.
