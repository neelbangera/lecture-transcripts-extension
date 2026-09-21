package markdown

import (
	"net/url"
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

func Render(job protocol.TranscriptJob) []byte {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("course: " + yamlScalar(job.CourseName) + "\n")
	builder.WriteString("term: " + yamlScalar(job.Term) + "\n")
	builder.WriteString("lecture: " + strconv.Itoa(job.LectureNumber) + "\n")
	builder.WriteString("date: " + yamlScalar(job.LectureDate) + "\n")
	if source, ok := PublishSourceURL(job.SourceURL); ok {
		builder.WriteString("source_url: " + yamlScalar(source) + "\n")
	}
	builder.WriteString("captured_at: " + yamlScalar(job.CapturedAt) + "\n")
	builder.WriteString("transcript_sha256: " + yamlScalar(job.ContentHash) + "\n")
	builder.WriteString("---\n\n")
	builder.WriteString("## Transcript\n\n")
	builder.WriteString(sectionBody(job.Transcript))
	builder.WriteString("\n## Timestamped transcript\n\n")
	if strings.TrimSpace(job.TimestampedTranscript) == "" {
		builder.WriteString(NoTimestampedSource + "\n")
	} else {
		builder.WriteString(sectionBody(job.TimestampedTranscript))
	}
	return []byte(builder.String())
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
