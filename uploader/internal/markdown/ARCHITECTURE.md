# markdown Architecture

## Purpose

`uploader/internal/markdown` renders the two deterministic Markdown documents
that the uploader publishes: the plain transcript (`## Transcript`) and the
timestamped transcript (`## Timestamped transcript`). Both documents end with
the same bottom metadata block whose `transcript_sha256` equals the job's
`contentHash`. The package also applies the publish-time source-URL
sanitization policy, which is the last line of defense before page-derived data
reaches a repository that may become public.

## Boundaries and dependencies

- Imports `net/url`, `regexp`, `strconv`, `strings`, and the uploader's
  `protocol` package for `TranscriptJob`.
- Consumers: `processor.processJob` calls `RenderPlain` for the plain path and
  `RenderTimestamped` for the timestamped path. The queue stores the canonical
  source URL; this package never re-canonicalizes arbitrary URLs, it decides
  whether the canonical value may be published.
- The renderer is pure and deterministic: no clock, no network, no filesystem,
  no config, no logging. The same job always produces byte-identical output
  (`TestRenderIsDeterministic`).

## Contracts and invariants

### The two documents

Plain (`RenderPlain(job)`):

```markdown
## Transcript

<transcript body>

---
course: 'EECS 491'
term: '2026-winter'
lecture: 6
date: '2026-02-12'
source_url: 'https://leccap.engin.umich.edu/lecture/123'
captured_at: '2026-02-12T18:03:22Z'
transcript_sha256: '0123...cdef'
---
```

Timestamped (`RenderTimestamped(job)`): identical structure with the heading
`## Timestamped transcript` and the timestamped body. When the timestamped
form is empty, the body is exactly the placeholder constant
`NoTimestampedSource = "_No timestamped source; see Transcript._"` and no
timestamps are invented.

The metadata block:

- is always at the bottom of the document, opened and closed by `---` lines;
- contains exactly these fields in this order: `course`, `term`, `lecture`
  (integer, not padded), `date`, optional `source_url`, `captured_at`,
  `transcript_sha256`;
- omits `source_url` entirely when `PublishSourceURL` rejects the value;
- `transcript_sha256` is the job `contentHash` rendered as a YAML scalar, so
  the write-once comparison in `github.ParseTranscriptHash` can recover it.

### One line per timestamp entry

`oneLinePerTimestamp`:

- normalizes CRLF to LF, drops blank lines;
- a line matching `^\s*\[\d{1,2}:\d{2}(?::\d{2})?\]` starts a new entry;
- the first non-blank line starts the first entry even if it does not match;
- every other line is appended to the previous entry joined by a single space;
- entries are rejoined with `\n`.

`TestRenderTimestampedOneLinePerEntry` proves
`"[00:01] first part\nsecond part\n[00:02] next entry\n[00:03] third"` becomes
`"[00:01] first part second part\n[00:02] next entry\n[00:03] third"`.

### YAML quoting

`yamlScalar` emits every metadata value as a single-quoted scalar and:

- replaces CRLF, LF, and CR with a single space so page-derived text cannot
  break the block structure;
- doubles internal single quotes (`'` → `''`).

`TestRenderSanitizesMetadata` asserts `EECS "491" it's` becomes
`course: 'EECS "491" it''s'` and that `EECS\n491\r\nWinter` becomes
`course: 'EECS 491 Winter'` while the field count stays at seven.

### Source-URL publish sanitization

```go
func PublishSourceURL(raw string) (string, bool)
```

Publish (`true`) only when all of the following hold:

- `url.Parse` succeeds and the URL has no userinfo, no opaque form, no query,
  and no fragment;
- scheme is `https` (case-insensitive);
- hostname is exactly `leccap.engin.umich.edu` (case-insensitive);
- port is empty or `443`;
- after splitting the escaped path on `/`, `.`, `_`, and `-`, no whole
  lowercased segment is in `{token, session, auth, sid}` (so `author` is
  allowed, `auth-or` and `session-id` are not);
- an empty path is normalized to `/`.

The returned URL is always `https://leccap.engin.umich.edu` plus the escaped
path, so an uppercase host in the stored value is lowercased on publish.
`TestPublishSourceURL` covers canonical, uppercase host, default port, empty
path, each sensitive segment, hyphenated sensitive segments, case-insensitive
segments, query, fragment, non-default port, `http`, other host, userinfo,
empty, and non-URL inputs.

### Exact signatures

```go
const NoTimestampedSource = "_No timestamped source; see Transcript._"

func RenderPlain(job protocol.TranscriptJob) []byte
func RenderTimestamped(job protocol.TranscriptJob) []byte
func PublishSourceURL(raw string) (string, bool)
```

Unexported helpers: `writeMetadata`, `oneLinePerTimestamp`, `yamlScalar`,
`sectionBody`. `sectionBody` trims trailing CR/LF and appends exactly one
newline, so a section never produces triple newlines and the rendered document
always ends with the metadata block's closing `---\n`.

## Data flow

```
queue job (canonical source URL)
  └─ processor.processJob
       ├─ markdown.RenderPlain(job)        -> plain path PUT
       └─ markdown.RenderTimestamped(job)  -> timestamped path PUT
            └─ PublishSourceURL decides whether source_url is emitted
                 └─ github.Client.Publish -> base64 body -> GitHub
```

The renderer never mutates the job; the content hash stays the job's
`contentHash` regardless of what is rendered or omitted.

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `render.go` | Deterministic plain/timestamped rendering, bottom metadata block, YAML quoting, one-line-per-entry joining, source-URL policy | `NoTimestampedSource`, `RenderPlain`, `RenderTimestamped`, `PublishSourceURL` |
| `render_test.go` | Golden-byte and policy tests | `TestRenderPlainGoldenBytes`, `TestRenderTimestampedGoldenBytes`, `TestRenderTimestampedOneLinePerEntry`, `TestRenderTimestampedWithoutSource`, `TestRenderOmitsUnsafeSourceURL`, `TestRenderSanitizesMetadata`, `TestRenderIsDeterministic`, `TestRenderNormalizesSectionTrailingNewlines`, `TestPublishSourceURL` |

## Testing and verification

Run `cd uploader && go test ./internal/markdown/ -count=1`. The golden tests
(`TestRenderPlainGoldenBytes`, `TestRenderTimestampedGoldenBytes`) pin the
exact bytes of both documents for `goldenJob()`, including the seven metadata
lines and the closing `---`. `TestRenderTimestampedWithoutSource` pins the
placeholder text. `TestRenderOmitsUnsafeSourceURL` proves that a URL with
`token`/`session` path segments and a query never appears in either document.
`TestRenderNormalizesSectionTrailingNewlines` proves trailing newline and CR
handling. `TestRenderIsDeterministic` repeats rendering and checks the closing
block.

## Related plan sections

- `TECHNICAL_PLAN.md` → "Stage 6" items 1–2 (two documents, bottom metadata
  block, one line per timestamp entry, placeholder, source-URL policy).
- `TECHNICAL_PLAN.md` → "Transcript job and state model" (source-URL rules:
  omit when a path segment matches `token|session|auth|sid`).
- `TECHNICAL_PLAN.md` → "Normative implementation contracts → Canonical
  source-URL vectors".
- `docs/SECURITY.md` → "Source-URL sanitization", "Remote-hash trust",
  "Private versus public repository".

## How to change this package safely

1. Keep the metadata block at the bottom and keep the field order; the GitHub
   preflight parser accepts the legacy top block but new files must use the
   bottom block.
2. Never write page-derived text unquoted or outside the metadata block;
   `yamlScalar` is the only scalar emitter.
3. Never duplicate one transcript form into the other; an empty timestamped
   form renders the placeholder only.
4. Keep `transcript_sha256` exactly the job `contentHash`. Do not re-hash the
   rendered body: the remote field is the write-once contract.
5. Any change to the golden bytes requires updating both golden tests and, if
   the format changes materially, `github.ParseTranscriptHash` and the
   processor write-once fixtures.
6. Do not add a third document or an alternate layout; the plan fixes one
   combined artifact per transcript form.

Open question (unverified against the plan text):

- `oneLinePerTimestamp` recognizes only bracket-form entries
  (`[MM:SS]`/`[H:MM:SS]`), while the normative normalization/derivation regex
  in `TECHNICAL_PLAN.md` is broader
  (`^\s*[\[(]?\d{1,2}:\d{2}(?::\d{2})?[\]\)]?\s*(?:[-–—|]\s*)?`). The Stage 0
  serialization contract always emits `[<time>] <text>`, so in practice the
  bracket form is what reaches the renderer; a non-bracket timestamped form
  would be merged into the previous entry rather than starting a new one. The
  protocol validator does not enforce the bracket form, so this is a latent
  edge case rather than a contradiction with observed input.
