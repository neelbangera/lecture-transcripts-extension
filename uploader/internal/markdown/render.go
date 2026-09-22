package markdown

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

const NoTimestampedSource = "_No timestamped source; see Transcript._"

var sensitiveSegments = map[string]struct{}{
	"token":   {},
	"session": {},
	"auth":    {},
	"sid":     {},
}

var timestampEntryPattern = regexp.MustCompile(`^\s*\[\d{1,2}:\d{2}(?::\d{2})?\]`)

// RenderPlain renders the plain transcript document. The metadata block sits
// at the bottom so the transcript reads first and remains self-contained.
func RenderPlain(job protocol.TranscriptJob) []byte {
	var builder strings.Builder
	builder.WriteString("## Transcript\n\n")
	builder.WriteString(sectionBody(job.Transcript))
	writeMetadata(&builder, job)
	return []byte(builder.String())
}

// RenderTimestamped renders the timestamped transcript document with exactly
// one line per timestamp entry, followed by the same metadata block.
func RenderTimestamped(job protocol.TranscriptJob) []byte {
	var builder strings.Builder
	builder.WriteString("## Timestamped transcript\n\n")
	if strings.TrimSpace(job.TimestampedTranscript) == "" {
		builder.WriteString(NoTimestampedSource + "\n")
	} else {
		builder.WriteString(sectionBody(oneLinePerTimestamp(job.TimestampedTranscript)))
	}
	writeMetadata(&builder, job)
	return []byte(builder.String())
}

func writeMetadata(builder *strings.Builder, job protocol.TranscriptJob) {
	builder.WriteString("\n---\n")
	builder.WriteString("course: " + yamlScalar(job.CourseName) + "\n")
	builder.WriteString("term: " + yamlScalar(job.Term) + "\n")
	builder.WriteString("lecture: " + strconv.Itoa(job.LectureNumber) + "\n")
	builder.WriteString("date: " + yamlScalar(job.LectureDate) + "\n")
	if source, ok := PublishSourceURL(job.SourceURL); ok {
		builder.WriteString("source_url: " + yamlScalar(source) + "\n")
	}
	builder.WriteString("captured_at: " + yamlScalar(job.CapturedAt) + "\n")
	builder.WriteString("transcript_sha256: " + yamlScalar(job.ContentHash) + "\n")
	builder.WriteString("---\n")
}

// oneLinePerTimestamp joins caption continuation lines onto their timestamp
// entry so every timestamped entry occupies exactly one line.
func oneLinePerTimestamp(value string) string {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	entries := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if timestampEntryPattern.MatchString(line) || len(entries) == 0 {
			entries = append(entries, trimmed)
			continue
		}
		last := len(entries) - 1
		entries[last] = entries[last] + " " + trimmed
	}
	return strings.Join(entries, "\n")
}

func PublishSourceURL(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "leccap.engin.umich.edu") {
		return "", false
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", false
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	for _, segment := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '.' || r == '_' || r == '-'
	}) {
		if _, found := sensitiveSegments[strings.ToLower(segment)]; found {
			return "", false
		}
	}
	return "https://leccap.engin.umich.edu" + path, true
}

func yamlScalar(value string) string {
	replacer := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "'", "''")
	return "'" + replacer.Replace(value) + "'"
}

func sectionBody(value string) string {
	return strings.TrimRight(value, "\r\n") + "\n"
}
