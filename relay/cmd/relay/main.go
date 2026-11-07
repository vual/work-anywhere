package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vual/work-anywhere/relay/internal/buildinfo"
	"github.com/vual/work-anywhere/relay/internal/config"
	"github.com/vual/work-anywhere/relay/internal/jobs"
	"github.com/vual/work-anywhere/relay/internal/observability"
	"github.com/vual/work-anywhere/relay/internal/push"
	"github.com/vual/work-anywhere/relay/internal/secretbox"
	"github.com/vual/work-anywhere/relay/internal/server"
	"github.com/vual/work-anywhere/relay/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: relay <serve|migrate|enrollment|version>")
	}
	switch args[0] {
	case "version":
		fmt.Printf("work-anywhere-relay %s revision=%s built=%s go=1.27.3\n", buildinfo.Version, buildinfo.Revision, buildinfo.BuildTime)
		return nil
	case "serve":
		return serve()
	case "migrate":
		if len(args) != 2 || (args[1] != "up" && args[1] != "status") {
			return errors.New("usage: relay migrate <up|status>")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		return store.Migrate(context.Background(), cfg.DatabaseURL, args[1])
	case "enrollment":
		return enrollment(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	state, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer state.Close()
	logger := observability.NewLogger(cfg.LogLevel)
	box, err := secretbox.New(cfg.DataKey)
	if err != nil {
		return err
	}
	sender, err := pushProviders(cfg)
	if err != nil {
		return err
	}
	queue, err := jobs.New(state.Pool, state, box, sender, logger)
	if err != nil {
		return err
	}
	if err = queue.Start(ctx); err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = queue.Stop(stopCtx)
	}()
	return server.New(cfg, state, logger, server.WithPushQueue(queue)).Run(ctx)
}

func pushProviders(cfg config.Config) (push.Sender, error) {
	providers := push.Multi{}
	if cfg.APNsKeyFile != "" {
		key, err := os.ReadFile(cfg.APNsKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read APNs key: %w", err)
		}
		providers.APNs, err = push.NewAPNs(push.APNsConfig{TeamID: cfg.APNsTeamID, KeyID: cfg.APNsKeyID, PrivateKeyPEM: key})
		if err != nil {
			return nil, err
		}
	}
	if cfg.FCMCredentialsFile != "" {
		credentials, err := os.ReadFile(cfg.FCMCredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("read FCM credentials: %w", err)
		}
		providers.FCM, err = push.NewFCM(credentials, nil)
		if err != nil {
			return nil, err
		}
	}
	return providers, nil
}

func enrollment(args []string) error {
	if len(args) == 0 || (args[0] != "create" && args[0] != "revoke") {
		return errors.New("usage: relay enrollment <create [--ttl 24h] [--uses 1]|revoke TOKEN>")
	}
	if args[0] == "revoke" {
		if len(args) != 2 {
			return errors.New("usage: relay enrollment revoke TOKEN")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		state, err := store.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer state.Close()
		return state.RevokeEnrollmentToken(context.Background(), args[1])
	}
	set := flag.NewFlagSet("enrollment create", flag.ContinueOnError)
	ttl := set.Duration("ttl", 24*time.Hour, "token validity")
	uses := set.Int("uses", 1, "maximum uses")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	if *ttl <= 0 || *uses <= 0 {
		return errors.New("ttl and uses must be positive")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	state, err := store.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer state.Close()
	plain, hash, err := store.NewEnrollmentToken()
	if err != nil {
		return err
	}
	if err = state.CreateEnrollmentToken(context.Background(), hash, time.Now().Add(*ttl), *uses); err != nil {
		return err
	}
	fmt.Println(plain)
	return nil
}
