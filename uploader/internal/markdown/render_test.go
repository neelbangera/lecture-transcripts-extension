package markdown

import (
	"strings"
	"testing"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

const goldenHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func goldenJob() protocol.TranscriptJob {
	return protocol.TranscriptJob{
		SchemaVersion:         1,
		Kind:                  "lecture",
		LectureKey:            "eecs491/2026-winter/006",
		CourseSlug:            "eecs491",
		CourseName:            "EECS 491",
		Term:                  "2026-winter",
		LectureNumber:         6,
		LectureDate:           "2026-02-12",
		SourceURL:             "https://leccap.engin.umich.edu/lecture/123",
		CapturedAt:            "2026-02-12T18:03:22Z",
		Transcript:            "Hello world.",
		TimestampedTranscript: "[00:01] Hello world.",
		ContentHash:           goldenHash,
	}
}

const goldenMetadata = `---
course: 'EECS 491'
term: '2026-winter'
lecture: 6
date: '2026-02-12'
source_url: 'https://leccap.engin.umich.edu/lecture/123'
captured_at: '2026-02-12T18:03:22Z'
transcript_sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
---
`

const goldenPlain = `## Transcript

Hello world.

` + goldenMetadata

const goldenTimestamped = `## Timestamped transcript

[00:01] Hello world.

` + goldenMetadata

func TestRenderPlainGoldenBytes(t *testing.T) {
	if got := string(RenderPlain(goldenJob())); got != goldenPlain {
		t.Fatalf("plain markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got, goldenPlain)
	}
}

func TestRenderTimestampedGoldenBytes(t *testing.T) {
	if got := string(RenderTimestamped(goldenJob())); got != goldenTimestamped {
		t.Fatalf("timestamped markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got, goldenTimestamped)
	}
}

func TestRenderTimestampedOneLinePerEntry(t *testing.T) {
	job := goldenJob()
	job.TimestampedTranscript = "[00:01] first part\nsecond part\n[00:02] next entry\n[00:03] third"
	want := "[00:01] first part second part\n[00:02] next entry\n[00:03] third"
	if got := oneLinePerTimestamp(job.TimestampedTranscript); got != want {
		t.Fatalf("oneLinePerTimestamp = %q, want %q", got, want)
	}
	rendered := string(RenderTimestamped(job))
	if !strings.Contains(rendered, want+"\n") {
		t.Fatalf("rendered timestamped body not one line per entry:\n%s", rendered)
	}
}

func TestRenderTimestampedWithoutSource(t *testing.T) {
	job := goldenJob()
	job.TimestampedTranscript = ""
	want := `## Timestamped transcript

_No timestamped source; see Transcript._

` + goldenMetadata
	if got := string(RenderTimestamped(job)); got != want {
		t.Fatalf("timestamped markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if NoTimestampedSource != "_No timestamped source; see Transcript._" {
		t.Fatalf("NoTimestampedSource = %q", NoTimestampedSource)
	}
}

func TestRenderOmitsUnsafeSourceURL(t *testing.T) {
	job := goldenJob()
	job.SourceURL = "https://leccap.engin.umich.edu/lecture/token/123?session=secret#frag"
	for _, got := range []string{string(RenderPlain(job)), string(RenderTimestamped(job))} {
		if strings.Contains(got, "source_url:") {
			t.Fatalf("unsafe source URL must be omitted:\n%s", got)
		}
		if strings.Contains(got, "token") || strings.Contains(got, "session") || strings.Contains(got, "secret") {
			t.Fatalf("source URL data leaked into markdown:\n%s", got)
		}
	}
}

func TestRenderSanitizesMetadata(t *testing.T) {
	job := goldenJob()
	job.CourseName = "EECS \"491\" it's"
	if got := string(RenderPlain(job)); !strings.Contains(got, "course: 'EECS \"491\" it''s'\n") {
		t.Fatalf("single quotes must be doubled:\n%s", got)
	}

	job = goldenJob()
	job.CourseName = "EECS\n491\r\nWinter"
	got := string(RenderPlain(job))
	if !strings.Contains(got, "course: 'EECS 491 Winter'\n") {
		t.Fatalf("newlines must not break metadata structure:\n%s", got)
	}
	parts := strings.Split(got, "\n---\n")
	fields := parts[len(parts)-2]
	if len(strings.Split(fields, "\n")) != 7 {
		t.Fatalf("metadata field count changed: %q", fields)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	job := goldenJob()
	first := RenderPlain(job)
	for i := 0; i < 5; i++ {
		if got := RenderPlain(job); string(got) != string(first) {
			t.Fatalf("plain render %d differs", i)
		}
	}
	if !strings.HasSuffix(string(first), "---\n") || strings.HasSuffix(string(first), "\n\n") {
		t.Fatalf("plain output must end with the metadata block: %q", string(first[len(first)-5:]))
	}

	firstTimestamped := RenderTimestamped(job)
	for i := 0; i < 5; i++ {
		if got := RenderTimestamped(job); string(got) != string(firstTimestamped) {
			t.Fatalf("timestamped render %d differs", i)
		}
	}
}

func TestRenderNormalizesSectionTrailingNewlines(t *testing.T) {
	job := goldenJob()
	job.Transcript = "Hello world.\n\n\n"
	job.TimestampedTranscript = "[00:01] Hello world.\r\n\r\n"
	if got := string(RenderPlain(job)); !strings.HasPrefix(got, "## Transcript\n\nHello world.\n\n---\n") {
		t.Fatalf("plain transcript trailing newlines not normalized:\n%q", got)
	}
	got := string(RenderTimestamped(job))
	if strings.Contains(got, "\r") {
		t.Fatalf("carriage returns must be trimmed from section bodies:\n%q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("rendered output must not contain triple newlines:\n%q", got)
	}
	if !strings.HasPrefix(got, "## Timestamped transcript\n\n[00:01] Hello world.\n\n---\n") {
		t.Fatalf("timestamped body not normalized:\n%q", got)
	}
}

func TestPublishSourceURL(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
		publish  bool
	}{
		{"canonical", "https://leccap.engin.umich.edu/lecture/123", "https://leccap.engin.umich.edu/lecture/123", true},
		{"uppercase host", "https://LECCAP.ENGIN.UMICH.EDU/lecture/123", "https://leccap.engin.umich.edu/lecture/123", true},
		{"default port", "https://leccap.engin.umich.edu:443/lecture/123", "https://leccap.engin.umich.edu/lecture/123", true},
		{"empty path", "https://leccap.engin.umich.edu", "https://leccap.engin.umich.edu/", true},
		{"token segment", "https://leccap.engin.umich.edu/lecture/token/123", "", false},
		{"session segment", "https://leccap.engin.umich.edu/lecture/session/123", "", false},
		{"auth segment", "https://leccap.engin.umich.edu/lecture/auth/123", "", false},
		{"sid segment", "https://leccap.engin.umich.edu/lecture/sid/123", "", false},
		{"hyphenated auth", "https://leccap.engin.umich.edu/lecture/auth-or/123", "", false},
		{"hyphenated session", "https://leccap.engin.umich.edu/lecture/session-id/123", "", false},
		{"author is allowed", "https://leccap.engin.umich.edu/lecture/author/123", "https://leccap.engin.umich.edu/lecture/author/123", true},
		{"case insensitive segment", "https://leccap.engin.umich.edu/lecture/TOKEN/123", "", false},
		{"query", "https://leccap.engin.umich.edu/lecture/123?session=secret", "", false},
		{"fragment", "https://leccap.engin.umich.edu/lecture/123#transcript", "", false},
		{"non default port", "https://leccap.engin.umich.edu:8443/lecture/123", "", false},
		{"http", "http://leccap.engin.umich.edu/lecture/123", "", false},
		{"other host", "https://example.com/lecture/123", "", false},
		{"userinfo", "https://user:pass@leccap.engin.umich.edu/lecture/123", "", false},
		{"empty", "", "", false},
		{"not a url", "not a url", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, publish := PublishSourceURL(tc.input)
			if publish != tc.publish || got != tc.expected {
				t.Fatalf("PublishSourceURL(%q) = %q, %v; want %q, %v", tc.input, got, publish, tc.expected, tc.publish)
			}
		})
	}
}
