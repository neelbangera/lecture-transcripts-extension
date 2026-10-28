package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/setup"
)

func runSetupCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("lecture-uploader --setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options setup.Options
	flags.StringVar(&options.AppClientID, "app-client-id", "", "verified public GitHub App client ID")
	flags.StringVar(&options.AppInstallURL, "app-install-url", "", "verified public GitHub App installation URL")
	flags.StringVar(&options.Repository, "repository", "", "GitHub repository URL or OWNER/REPO")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments after --setup options")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return setup.Run(ctx, options, stdin, stdout)
}
