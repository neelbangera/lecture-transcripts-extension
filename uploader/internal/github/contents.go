package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

// RemoteFile describes the bounded, privacy-safe result of inspecting one
// target path.  It never contains remote body text.
type RemoteFile struct {
	Path        string
	Kind        protocol.RemoteFileKind
	BlobSHA     string
	ContentHash *string
	Size        int64
}

// PublishOutcome is the write-once result for one job.
type PublishOutcome string

const (
	// OutcomeCreated means GitHub accepted a create-only PUT (no sha).
	OutcomeCreated PublishOutcome = "created"
	// OutcomeUnchanged means the remote file already carries the job hash.
	OutcomeUnchanged PublishOutcome = "unchanged"
	// OutcomeConflict means the path is occupied by different, malformed, or
	// non-file content.  The uploader never overwrites it.
	OutcomeConflict PublishOutcome = "conflict"
)

// PublishResult carries the classification the processor persists.
type PublishResult struct {
	Outcome    PublishOutcome
	Remote     RemoteFile
	HTTPStatus *int
}

// CommitMessage is the exact create-commit message for a job.
func CommitMessage(job protocol.TranscriptJob) string {
	return fmt.Sprintf("Add %s (%s lecture %d)", job.LectureKey, job.CourseName, job.LectureNumber)
}

var hashValuePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ParseTranscriptHash extracts the single transcript_sha256 scalar from the
// metadata block. New files carry the block at the bottom of the document; the
// legacy top block is still accepted so previously written files stay
// idempotent. It returns false for a missing, duplicated, unquoted-invalid, or
// malformed block so callers never trust an ambiguous remote file.
func ParseTranscriptHash(content []byte) (string, bool) {
	if len(content) == 0 {
		return "", false
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	if strings.TrimRight(lines[0], " \t") == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimRight(lines[i], " \t") == "---" {
				return parseHashBlock(lines[1:i])
			}
		}
		return "", false
	}

	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end == 0 || strings.TrimRight(lines[end-1], " \t") != "---" {
		return "", false
	}
	start := -1
	for i := end - 2; i >= 0; i-- {
		if strings.TrimRight(lines[i], " \t") == "---" {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	return parseHashBlock(lines[start+1 : end-1])
}

func parseHashBlock(lines []string) (string, bool) {
	hash := ""
	found := false
	for _, line := range lines {
		value, ok := parseHashLine(line)
		if !ok {
			continue
		}
		if found {
			return "", false
		}
		hash = value
		found = true
	}
	if !found {
		return "", false
	}
	return hash, true
}

func parseHashLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "transcript_sha256:") {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(trimmed, "transcript_sha256:"))
	value = strings.Trim(value, "'\"")
	if !hashValuePattern.MatchString(value) {
		return "", false
	}
	return value, true
}

// InspectFile performs the preflight GET for one repository path.  A 404 is
// reported as RemoteMissing rather than an error; every other non-success
// response is classified.
func (c *Client) InspectFile(ctx context.Context, path string) (RemoteFile, error) {
	query := url.Values{"ref": {c.branch}}
	response, err := c.do(ctx, http.MethodGet, c.contentsPath(path), query, nil)
	if err != nil {
		return RemoteFile{}, err
	}
	switch response.status {
	case http.StatusOK:
		return parseContentsResponse(path, response.body)
	case http.StatusNotFound:
		return RemoteFile{Path: path, Kind: protocol.RemoteMissing}, nil
	default:
		return RemoteFile{}, classifyHTTP("contents.get", response.status, response.header, response.body)
	}
}

// CreateResult is the accepted create response.
type CreateResult struct {
	Path      string
	BlobSHA   string
	CommitSHA string
}

// CreateFile issues a create-only PUT without a sha.  Only the documented 201
// response with a content object is accepted; every other response is an
// error, and no update request is ever attempted.
func (c *Client) CreateFile(ctx context.Context, path string, content []byte, message string) (CreateResult, error) {
	payload := struct {
		Message string `json:"message"`
		Content string `json:"content"`
		Branch  string `json:"branch"`
	}{
		Message: message,
		Content: base64.StdEncoding.EncodeToString(content),
		Branch:  c.branch,
	}
	response, err := c.do(ctx, http.MethodPut, c.contentsPath(path), nil, payload)
	if err != nil {
		return CreateResult{}, err
	}
	if response.status != http.StatusCreated {
		return CreateResult{}, classifyHTTP("contents.put", response.status, response.header, response.body)
	}
	var created struct {
		Content *struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
		} `json:"content"`
		Commit *struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(response.body, &created); err != nil || created.Content == nil || created.Content.SHA == "" {
		// A 201 without the documented shape is not proof of creation.  The
		// caller re-GETs the target; a retry is safe because the target may
		// still be absent.
		return CreateResult{}, newError("contents.put", CategoryRetryable, http.StatusCreated)
	}
	result := CreateResult{Path: path, BlobSHA: created.Content.SHA}
	if created.Commit != nil {
		result.CommitSHA = created.Commit.SHA
	}
	return result, nil
}

// Publish implements preflight-plus-create write-once publishing.  It never
// sends a sha and never assumes conditional-create semantics: any unexpected
// create response triggers a fresh GET so a race resolves to unchanged,
// conflict, or a classified error.
func (c *Client) Publish(ctx context.Context, path string, job protocol.TranscriptJob, content []byte) (PublishResult, error) {
	remote, err := c.InspectFile(ctx, path)
	if err != nil {
		return PublishResult{}, err
	}

	switch remote.Kind {
	case protocol.RemoteMissing:
		if _, createErr := c.CreateFile(ctx, path, content, CommitMessage(job)); createErr != nil {
			return c.resolveCreateFailure(ctx, path, job, createErr)
		}
		status := http.StatusCreated
		return PublishResult{Outcome: OutcomeCreated, Remote: remote, HTTPStatus: &status}, nil

	case protocol.RemoteFile:
		if remote.ContentHash != nil && *remote.ContentHash == job.ContentHash {
			status := http.StatusOK
			return PublishResult{Outcome: OutcomeUnchanged, Remote: remote, HTTPStatus: &status}, nil
		}
		return PublishResult{Outcome: OutcomeConflict, Remote: remote}, nil

	default:
		// directory, symlink, submodule, or malformed file
		return PublishResult{Outcome: OutcomeConflict, Remote: remote}, nil
	}
}

func (c *Client) resolveCreateFailure(ctx context.Context, path string, job protocol.TranscriptJob, createErr error) (PublishResult, error) {
	var classified *Error
	if errors.As(createErr, &classified) && classified.Category == CategoryAuth {
		// A 401 was already retried once with a forced refresh; a fresh GET
		// cannot change the answer, so surface the auth failure directly.
		return PublishResult{}, createErr
	}

	recheck, recheckErr := c.InspectFile(ctx, path)
	if recheckErr != nil {
		return PublishResult{}, recheckErr
	}
	switch recheck.Kind {
	case protocol.RemoteMissing:
		return PublishResult{}, createErr
	case protocol.RemoteFile:
		if recheck.ContentHash != nil && *recheck.ContentHash == job.ContentHash {
			status := http.StatusOK
			return PublishResult{Outcome: OutcomeUnchanged, Remote: recheck, HTTPStatus: &status}, nil
		}
		return PublishResult{Outcome: OutcomeConflict, Remote: recheck}, nil
	default:
		return PublishResult{Outcome: OutcomeConflict, Remote: recheck}, nil
	}
}

type contentsObject struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

func parseContentsResponse(path string, body []byte) (RemoteFile, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		// A JSON array means the path names a directory; it is never a
		// missing file.
		return RemoteFile{Path: path, Kind: protocol.RemoteDirectory}, nil
	}
	var object contentsObject
	if err := json.Unmarshal(body, &object); err != nil {
		return RemoteFile{}, newError("contents.get", CategoryPermanent, http.StatusOK)
	}
	switch object.Type {
	case "file":
		remote := RemoteFile{Path: path, Kind: protocol.RemoteFile, BlobSHA: object.SHA, Size: object.Size}
		hash, ok := decodeRemoteHash(object)
		if !ok {
			remote.Kind = protocol.RemoteMalformed
			return remote, nil
		}
		remote.ContentHash = &hash
		return remote, nil
	case "dir":
		return RemoteFile{Path: path, Kind: protocol.RemoteDirectory, BlobSHA: object.SHA}, nil
	case "symlink":
		return RemoteFile{Path: path, Kind: protocol.RemoteSymlink, BlobSHA: object.SHA}, nil
	case "submodule":
		return RemoteFile{Path: path, Kind: protocol.RemoteSubmodule, BlobSHA: object.SHA}, nil
	default:
		return RemoteFile{Path: path, Kind: protocol.RemoteMalformed, BlobSHA: object.SHA}, nil
	}
}

func decodeRemoteHash(object contentsObject) (string, bool) {
	if object.Encoding != "base64" || object.Content == "" {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(object.Content, "\n", ""))
	if err != nil {
		return "", false
	}
	return ParseTranscriptHash(decoded)
}
