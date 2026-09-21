package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/protocol"
)

const validConfigJSON = `{
  "schemaVersion": 1,
  "githubAppClientId": "Iv1.abc123def456",
  "repositoryId": 123456789,
  "owner": "neelbangera",
  "repo": "lecture-transcripts",
  "branch": "main"
}`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	cfg, err := LoadFrom(writeConfig(t, validConfigJSON))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.SchemaVersion != 1 || cfg.GitHubAppClientID != "Iv1.abc123def456" || cfg.RepositoryID != 123456789 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Owner != ExpectedOwner || cfg.Repo != ExpectedRepo || cfg.Branch != ExpectedBranch {
		t.Fatalf("unexpected target: %+v", cfg)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ``},
		{"malformed", `{"schemaVersion":`},
		{"wrong schema version", `{"schemaVersion":2,"githubAppClientId":"Iv1.abc123def456","repositoryId":1,"owner":"neelbangera","repo":"lecture-transcripts","branch":"main"}`},
		{"missing client id", `{"schemaVersion":1,"repositoryId":1,"owner":"neelbangera","repo":"lecture-transcripts","branch":"main"}`},
		{"placeholder client id", `{"schemaVersion":1,"githubAppClientId":"REPLACE_WITH_REAL_GITHUB_APP_CLIENT_ID","repositoryId":1,"owner":"neelbangera","repo":"lecture-transcripts","branch":"main"}`},
		{"short client id", `{"schemaVersion":1,"githubAppClientId":"abc","repositoryId":1,"owner":"neelbangera","repo":"lecture-transcripts","branch":"main"}`},
		{"client id with whitespace", `{"schemaVersion":1,"githubAppClientId":"Iv1 abc123","repositoryId":1,"owner":"neelbangera","repo":"lecture-transcripts","branch":"main"}`},
		{"zero repository id", `{"schemaVersion":1,"githubAppClientId":"Iv1.abc123def456","repositoryId":0,"owner":"neelbangera","repo":"lecture-transcripts","branch":"main"}`},
		{"negative repository id", `{"schemaVersion":1,"githubAppClientId":"Iv1.abc123def456","repositoryId":-5,"owner":"neelbangera","repo":"lecture-transcripts","branch":"main"}`},
		{"string repository id", `{"schemaVersion":1,"githubAppClientId":"Iv1.abc123def456","repositoryId":"1","owner":"neelbangera","repo":"lecture-transcripts","branch":"main"}`},
		{"wrong owner", `{"schemaVersion":1,"githubAppClientId":"Iv1.abc123def456","repositoryId":1,"owner":"someoneelse","repo":"lecture-transcripts","branch":"main"}`},
		{"wrong repo", `{"schemaVersion":1,"githubAppClientId":"Iv1.abc123def456","repositoryId":1,"owner":"neelbangera","repo":"other","branch":"main"}`},
		{"wrong branch", `{"schemaVersion":1,"githubAppClientId":"Iv1.abc123def456","repositoryId":1,"owner":"neelbangera","repo":"lecture-transcripts","branch":"dev"}`},
		{"unknown field", `{"schemaVersion":1,"githubAppClientId":"Iv1.abc123def456","repositoryId":1,"owner":"neelbangera","repo":"lecture-transcripts","branch":"main","token":"secret"}`},
		{"trailing value", validConfigJSON + ` {}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadFrom(writeConfig(t, tc.content)); err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
		})
	}
}

func TestLoadRejectsMissingFile(t *testing.T) {
	if _, err := LoadFrom(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected missing config to fail closed")
	}
}

func TestExampleConfigIsNonfunctional(t *testing.T) {
	data, err := os.ReadFile("config.example.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("example is not JSON: %v", err)
	}
	wantKeys := []string{"schemaVersion", "githubAppClientId", "repositoryId", "owner", "repo", "branch"}
	if len(raw) != len(wantKeys) {
		t.Fatalf("example has %d keys, want %d", len(raw), len(wantKeys))
	}
	for _, key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("example missing key %q", key)
		}
	}
	for key := range raw {
		switch key {
		case "schemaVersion", "githubAppClientId", "repositoryId", "owner", "repo", "branch":
		default:
			t.Fatalf("example contains unexpected key %q", key)
		}
	}

	cfg, err := decode(data)
	if err != nil {
		t.Fatalf("example must decode structurally: %v", err)
	}
	if cfg.RepositoryID != 0 {
		t.Fatalf("example repositoryId = %d, want 0 placeholder", cfg.RepositoryID)
	}
	if cfg.Owner == ExpectedOwner || cfg.Repo == ExpectedRepo || cfg.Branch == ExpectedBranch {
		t.Fatalf("example must not contain real target values: %+v", cfg)
	}
	if !strings.Contains(strings.ToUpper(cfg.GitHubAppClientID), "REPLACE") {
		t.Fatalf("example client id is not a placeholder: %q", cfg.GitHubAppClientID)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("example must be invalid at runtime")
	}
	if _, err := LoadFrom("config.example.json"); err == nil {
		t.Fatal("example must fail closed when loaded")
	}
}

func TestPathsForHome(t *testing.T) {
	home := "/Users/example"
	paths := PathsFor(home)
	base := filepath.Join(home, "Library", "Application Support", "LectureTranscripts")
	logs := filepath.Join(home, "Library", "Logs", "LectureTranscripts")
	if paths.Dir != base {
		t.Fatalf("Dir = %q, want %q", paths.Dir, base)
	}
	if paths.Config != filepath.Join(base, "config.json") {
		t.Fatalf("Config = %q", paths.Config)
	}
	if paths.Queue != filepath.Join(base, "queue.sqlite3") {
		t.Fatalf("Queue = %q", paths.Queue)
	}
	if paths.Lock != filepath.Join(base, "queue.lock") {
		t.Fatalf("Lock = %q", paths.Lock)
	}
	if paths.LogDir != logs {
		t.Fatalf("LogDir = %q, want %q", paths.LogDir, logs)
	}
	if paths.Log != filepath.Join(logs, "uploader.log") {
		t.Fatalf("Log = %q", paths.Log)
	}
}

func TestDefaultPathsUseHomeEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	if paths != PathsFor(home) {
		t.Fatalf("DefaultPaths = %+v, want %+v", paths, PathsFor(home))
	}
	if !strings.HasPrefix(paths.Config, home) {
		t.Fatalf("config path escaped test home: %q", paths.Config)
	}
}

func TestLimitsMatchProtocolContract(t *testing.T) {
	if MaxSerializedJobBytes != protocol.MaxSerializedJobByte {
		t.Fatalf("MaxSerializedJobBytes = %d, want %d", MaxSerializedJobBytes, protocol.MaxSerializedJobByte)
	}
	if MaxTranscriptBytes != protocol.MaxTranscriptBytes {
		t.Fatalf("MaxTranscriptBytes = %d, want %d", MaxTranscriptBytes, protocol.MaxTranscriptBytes)
	}
	if MaxSourceURLBytes != protocol.MaxSourceURLBytes {
		t.Fatalf("MaxSourceURLBytes = %d, want %d", MaxSourceURLBytes, protocol.MaxSourceURLBytes)
	}
}

func TestBackoffScheduleContract(t *testing.T) {
	want := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}
	got := BackoffSchedule()
	if len(got) != len(want) {
		t.Fatalf("schedule length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("schedule[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	if BackoffJitterFraction != 0.20 {
		t.Fatalf("BackoffJitterFraction = %v, want 0.20", BackoffJitterFraction)
	}
	got[0] = time.Nanosecond
	if BackoffSchedule()[0] != want[0] {
		t.Fatal("BackoffSchedule must return a fresh slice")
	}
}

func TestQueueAndLoggingLimits(t *testing.T) {
	if QueueMaxJobs != 500 {
		t.Fatalf("QueueMaxJobs = %d, want 500", QueueMaxJobs)
	}
	if QueueMaxBytes != 100<<20 {
		t.Fatalf("QueueMaxBytes = %d, want 100 MiB", QueueMaxBytes)
	}
	if QueueRetention != 7*24*time.Hour {
		t.Fatalf("QueueRetention = %s, want 7 days", QueueRetention)
	}
	if UploadLease != 10*time.Minute {
		t.Fatalf("UploadLease = %s, want 10m", UploadLease)
	}
	if LogMaxBytes != 5<<20 || LogMaxFiles != 3 {
		t.Fatalf("log limits = %d bytes/%d files", LogMaxBytes, LogMaxFiles)
	}
	if MaxRenderTimeMs != 30000 {
		t.Fatalf("MaxRenderTimeMs = %d, want 30000", MaxRenderTimeMs)
	}
}
