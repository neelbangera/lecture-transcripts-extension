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

const goldenRendered = `---
course: 'EECS 491'
term: '2026-winter'
lecture: 6
date: '2026-02-12'
source_url: 'https://leccap.engin.umich.edu/lecture/123'
captured_at: '2026-02-12T18:03:22Z'
transcript_sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
---

## Transcript

Hello world.

## Timestamped transcript

[00:01] Hello world.
`

func TestRenderGoldenBytes(t *testing.T) {
	got := string(Render(goldenJob()))
	if got != goldenRendered {
		t.Fatalf("rendered markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got, goldenRendered)
	}
}

func TestRenderWithoutTimestampedSource(t *testing.T) {
	job := goldenJob()
	job.TimestampedTranscript = ""
	want := `---
course: 'EECS 491'
term: '2026-winter'
lecture: 6
date: '2026-02-12'
source_url: 'https://leccap.engin.umich.edu/lecture/123'
captured_at: '2026-02-12T18:03:22Z'
transcript_sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
---

## Transcript

Hello world.

## Timestamped transcript

_No timestamped source; see Transcript._
`
	if got := string(Render(job)); got != want {
		t.Fatalf("rendered markdown mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if NoTimestampedSource != "_No timestamped source; see Transcript._" {
		t.Fatalf("NoTimestampedSource = %q", NoTimestampedSource)
	}
}

func TestRenderOmitsUnsafeSourceURL(t *testing.T) {
	job := goldenJob()
	job.SourceURL = "https://leccap.engin.umich.edu/lecture/token/123?session=secret#frag"
	got := string(Render(job))
	if strings.Contains(got, "source_url:") {
		t.Fatalf("unsafe source URL must be omitted:\n%s", got)
	}
	if strings.Contains(got, "token") || strings.Contains(got, "session") || strings.Contains(got, "secret") {
		t.Fatalf("source URL data leaked into markdown:\n%s", got)
	}
}

func TestRenderSanitizesFrontmatterMetadata(t *testing.T) {
	job := goldenJob()
	job.CourseName = "EECS \"491\" it's"
	job.Term = "2026-winter"
	got := string(Render(job))
	if !strings.Contains(got, "course: 'EECS \"491\" it''s'\n") {
		t.Fatalf("single quotes must be doubled:\n%s", got)
	}

	job = goldenJob()
	job.CourseName = "EECS\n491\r\nWinter"
	got = string(Render(job))
	if !strings.Contains(got, "course: 'EECS 491 Winter'\n") {
		t.Fatalf("newlines must not break frontmatter structure:\n%s", got)
	}
	frontmatter := strings.SplitN(strings.TrimPrefix(got, "---\n"), "\n---\n", 2)[0]
	if len(strings.Split(frontmatter, "\n")) != 7 {
		t.Fatalf("frontmatter line count changed: %q", frontmatter)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	job := goldenJob()
	first := Render(job)
	for i := 0; i < 5; i++ {
		if got := Render(job); string(got) != string(first) {
			t.Fatalf("render %d differs", i)
		}
	}
	if !strings.HasSuffix(string(first), "\n") || strings.HasSuffix(string(first), "\n\n") {
		t.Fatalf("rendered output must end in exactly one newline: %q", string(first[len(first)-3:]))
	}
}

func TestRenderNormalizesSectionTrailingNewlines(t *testing.T) {
	job := goldenJob()
	job.Transcript = "Hello world.\n\n\n"
	job.TimestampedTranscript = "[00:01] Hello world.\r\n\r\n"
	got := string(Render(job))
	if !strings.Contains(got, "Hello world.\n\n## Timestamped transcript") {
		t.Fatalf("plain transcript trailing newlines not normalized:\n%q", got)
	}
	if strings.Contains(got, "\r") {
		t.Fatalf("carriage returns must be trimmed from section bodies:\n%q", got)
	}
	if strings.Count(got, "\n\n\n") != 0 {
		t.Fatalf("rendered output must not contain triple newlines:\n%q", got)
	}
	if !strings.HasSuffix(got, "[00:01] Hello world.\n") {
		t.Fatalf("rendered output must end with the timestamped body:\n%q", got)
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
