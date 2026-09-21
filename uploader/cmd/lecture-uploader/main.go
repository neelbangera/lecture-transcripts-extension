// Command lecture-uploader is the macOS Native Messaging host for the Lecture
// Transcripts extension.  It owns the durable queue, GitHub authorization, and
// write-once publishing, and writes only framed protocol data to stdout.
// Diagnostics go to the sanitized rotated log, never to stdout.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/auth"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/github"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/host"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/logging"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/processor"
	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/queue"
)

// version is injected by scripts/build-uploader.sh with
// -ldflags "-X main.version=...".  Keep the name and declaration stable.
var version = "dev"

const hostManifestName = "com.neelbangera.lecturetranscripts.json"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "lecture-uploader: %v\n", err)
		os.Exit(1)
	}
}

// run assembles the tested components in dependency order.  Every configuration,
// lock, queue, credential, and origin failure exits before a protocol byte is
// written, so a misconfigured host can never serve a partial session.
func run(args []string, stdin io.Reader, stdout io.Writer) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}

	// Logging is best-effort at startup: the nil-safe logger simply drops
	// diagnostics if the log directory cannot be created, while stdout stays
	// reserved for framed protocol data.
	logger, _ := logging.Open(paths.Log)
	logger.Info(logging.EventStartup, logging.Fields{})

	cfg, err := config.LoadFrom(paths.Config)
	if err != nil {
		return logFatal(logger, logging.EventConfigLoaded, err)
	}
	logger.Info(logging.EventConfigLoaded, logging.Fields{})

	lock, err := queue.AcquireLock(paths.Lock)
	if err != nil {
		if errors.Is(err, queue.ErrAlreadyRunning) {
			return logFatal(logger, logging.EventLockAlreadyRunning, err)
		}
		return logFatal(logger, logging.EventLockAcquired, err)
	}
	defer lock.Release()
	logger.Info(logging.EventLockAcquired, logging.Fields{})

	store, err := queue.Open(paths.Queue)
	if err != nil {
		return logFatal(logger, logging.EventQueueOpened, err)
	}
	defer store.Close()
	logger.Info(logging.EventQueueOpened, logging.Fields{})

	credentialStore, err := auth.NewStore()
	if err != nil {
		return logFatal(logger, logging.EventAuthState, err)
	}
	authManager, err := auth.NewManager(credentialStore, auth.Config{
		ClientID:     cfg.GitHubAppClientID,
		RepositoryID: cfg.RepositoryID,
		Owner:        cfg.Owner,
		Repo:         cfg.Repo,
		Branch:       cfg.Branch,
	})
	if err != nil {
		return logFatal(logger, logging.EventAuthState, err)
	}

	client, err := github.NewClient(github.ClientConfig{
		Owner:       cfg.Owner,
		Repo:        cfg.Repo,
		Branch:      cfg.Branch,
		Credentials: authManager,
	})
	if err != nil {
		return logFatal(logger, logging.EventGitHubRequest, err)
	}

	proc, err := processor.New(processor.Config{
		Store:     store,
		Auth:      authManager,
		Publisher: client,
		Logger:    logger,
		Version:   version,
	})
	if err != nil {
		return logFatal(logger, logging.EventStartup, err)
	}

	origin, err := host.FormatOriginArgument(args)
	if err != nil {
		return logFatal(logger, logging.EventProtocolError, err)
	}
	allowedOrigin, err := readAllowedOrigin()
	if err != nil {
		return logFatal(logger, logging.EventProtocolError, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := host.NewServer(stdin, stdout, proc, proc, origin, allowedOrigin)
	runErr := server.Run(ctx)
	logger.Info(logging.EventShutdown, logging.Fields{})

	switch {
	case runErr == nil, errors.Is(runErr, io.EOF), errors.Is(runErr, context.Canceled):
		return nil
	default:
		return runErr
	}
}

// readAllowedOrigin reads the exact origin rendered by
// scripts/install-native-host.sh from the installed Chrome host manifest.
// The manifest is the only configured source of the allowed origin; a missing,
// multi-origin, or malformed registration fails closed.
func readAllowedOrigin() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	manifestPath := filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts", hostManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("read native messaging host manifest: %w", err)
	}
	var manifest struct {
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("decode native messaging host manifest: %w", err)
	}
	if len(manifest.AllowedOrigins) != 1 {
		return "", errors.New("native messaging host manifest must declare exactly one allowed origin")
	}
	allowed := manifest.AllowedOrigins[0]
	if err := host.ValidateOrigin(allowed, allowed); err != nil {
		return "", fmt.Errorf("invalid allowed origin in native messaging host manifest: %w", err)
	}
	return allowed, nil
}

func logFatal(logger *logging.Logger, event logging.Event, err error) error {
	logger.Error(event, logging.Fields{})
	return err
}
