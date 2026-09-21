package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	SchemaVersion = 1

	MaxSerializedJobBytes = 972800
	MaxTranscriptBytes    = 460800
	MaxSourceURLBytes     = 2048
	MaxRenderTimeMs       = 30000

	QueueMaxJobs   = 500
	QueueMaxBytes  = 100 << 20
	QueueRetention = 7 * 24 * time.Hour
	UploadLease    = 10 * time.Minute

	LogMaxBytes = 5 << 20
	LogMaxFiles = 3

	BackoffJitterFraction = 0.20

	ExpectedOwner  = "neelbangera"
	ExpectedRepo   = "lecture-transcripts"
	ExpectedBranch = "main"

	AppDirName     = "LectureTranscripts"
	LogDirName     = "LectureTranscripts"
	ConfigFileName = "config.json"
	QueueFileName  = "queue.sqlite3"
	LockFileName   = "queue.lock"
	LogFileName    = "uploader.log"
)

func BackoffSchedule() []time.Duration {
	return []time.Duration{
		5 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
		time.Hour,
	}
}

type Config struct {
	SchemaVersion     int    `json:"schemaVersion"`
	GitHubAppClientID string `json:"githubAppClientId"`
	RepositoryID      int64  `json:"repositoryId"`
	Owner             string `json:"owner"`
	Repo              string `json:"repo"`
	Branch            string `json:"branch"`
}

type Paths struct {
	Dir    string
	Config string
	Queue  string
	Lock   string
	LogDir string
	Log    string
}

func PathsFor(homeDir string) Paths {
	dir := filepath.Join(homeDir, "Library", "Application Support", AppDirName)
	logDir := filepath.Join(homeDir, "Library", "Logs", LogDirName)
	return Paths{
		Dir:    dir,
		Config: filepath.Join(dir, ConfigFileName),
		Queue:  filepath.Join(dir, QueueFileName),
		Lock:   filepath.Join(dir, LockFileName),
		LogDir: logDir,
		Log:    filepath.Join(logDir, LogFileName),
	}
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return Paths{}, errors.New("resolve home directory: empty home directory")
	}
	return PathsFor(home), nil
}

func Load() (Config, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(paths.Config)
}

func LoadFrom(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read machine-local config: %w", err)
	}
	cfg, err := decode(data)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid machine-local config: %w", err)
	}
	return cfg, nil
}

func decode(data []byte) (Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode machine-local config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("decode machine-local config: multiple JSON values")
		}
		return Config{}, fmt.Errorf("decode machine-local config: %w", err)
	}
	return cfg, nil
}

var (
	clientIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{7,127}$`)
	placeholderMarkers = []string{
		"replace",
		"placeholder",
		"changeme",
		"your_",
		"example",
	}
)

func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion must be %d", SchemaVersion)
	}
	if !clientIDPattern.MatchString(c.GitHubAppClientID) {
		return errors.New("githubAppClientId is missing or malformed")
	}
	lowered := strings.ToLower(c.GitHubAppClientID)
	for _, marker := range placeholderMarkers {
		if strings.Contains(lowered, marker) {
			return errors.New("githubAppClientId is a nonfunctional placeholder")
		}
	}
	if c.RepositoryID <= 0 {
		return errors.New("repositoryId must be a positive integer")
	}
	if c.Owner != ExpectedOwner {
		return fmt.Errorf("owner must be %q", ExpectedOwner)
	}
	if c.Repo != ExpectedRepo {
		return fmt.Errorf("repo must be %q", ExpectedRepo)
	}
	if c.Branch != ExpectedBranch {
		return fmt.Errorf("branch must be %q", ExpectedBranch)
	}
	return nil
}
