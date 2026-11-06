// Command dumbo-channel is the Dumbo channel: it carries bind-call
// connections, calling pages on web servers when core asks over the control
// link (docs: contracts/channel-core-connections/).
package main

import (
	"context"
	_ "embed"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/logoscore/logos-dumbo/internal/caller"
	"github.com/logoscore/logos-dumbo/internal/config"
	"github.com/logoscore/logos-dumbo/internal/connection"
	"github.com/logoscore/logos-dumbo/internal/management"
	"github.com/logoscore/logos-golang-channel-core/v3/pkg/channel"
	"github.com/logoscore/logos-golang-channel-core/v3/pkg/linkclient"
	"github.com/logoscore/logos-golang-module-core/pkg/lifecycle"
	"github.com/logoscore/logos-golang-module-core/pkg/logging"
	"github.com/logoscore/logos-golang-module-core/pkg/logstream"
	"github.com/logoscore/logos-golang-module-core/pkg/module"
)

// version is reported at registration; set with -ldflags "-X main.version=...".
var version = "dev"

// icon is the channel's icon in core's UI, sent at registration: a baby
// elephant, sitting (the elephant is PHP's), traced from a line drawing.
// The app paints an icon in one colour, so for dark themes it is the
// filled figure with the drawing's lines cut out of it.
//
//go:embed icon.svg
var icon string

// iconLight is the same drawing for light themes, where the filled figure
// would read as a negative: its lines are the shape.
//
//go:embed icon-light.svg
var iconLight string

func main() {
	var envFile string
	flag.StringVar(&envFile, "config", ".env", "path to .env fallback file")
	flag.Parse()
	cfg, err := config.LoadFromEnv(envFile)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Logs go to stderr at the managed log_level, and to core's live log
	// tail through the hub.
	var level slog.LevelVar
	logs := logstream.New(cfg.ChannelID)
	logger, err := logging.New(cfg.LogFormat, os.Stderr, &level, logs)
	if err != nil {
		log.Fatalf("logging: %v", err)
	}
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	calls := caller.New(caller.Settings{Timeout: cfg.CallTimeout, MaxBodyBytes: cfg.MaxBodyBytes})
	mgmtSrv, err := management.New(cfg, calls, &level)
	if err != nil {
		slog.Warn("management disabled", "err", err)
	}
	slog.Info("logos-dumbo-channel starting", "channel_id", cfg.ChannelID, "core", cfg.CoreBaseURL,
		"call_timeout", cfg.CallTimeout.String(), "max_body_bytes", cfg.MaxBodyBytes)

	// The control-plane session outlives ctx: calls in progress finish while
	// core still treats this instance as registered.
	regCtx, stopReg := context.WithCancel(context.Background())
	regDone := make(chan struct{})
	go func() {
		defer close(regDone)
		schema := connection.Schema()
		mc := channel.Config{
			Config: module.Config{
				AMQPURL: cfg.RabbitMQURL,
				Identity: lifecycle.Identity{
					ModuleType:  channel.ModuleType,
					ModuleName:  "dumbo",
					Instance:    cfg.ChannelID,
					Version:     version,
					Description: "Calls pages on web servers (bind-call): core reaches a PHP page Dumbo built whenever it has something to send",
					Icon:        icon,
					IconLight:   iconLight,
				},
			},
			Connection: &schema,
		}
		if mgmtSrv != nil {
			mc.Management, mc.Logs = mgmtSrv, logs
		}
		if err := channel.Run(regCtx, mc); err != nil {
			fatal("control plane failed", "err", err)
		}
	}()

	// The control link: core refuses it until the instance is registered,
	// and the client keeps trying.
	linkCtx, stopLink := context.WithCancel(context.Background())
	linkDone := make(chan struct{})
	go func() {
		defer close(linkDone)
		err := linkclient.Run(linkCtx, linkclient.Options{
			BaseURL: cfg.CoreBaseURL, Instance: cfg.ChannelID, Handler: calls.Call, OnState: calls.SetLinkUp,
		})
		if err != nil {
			fatal("control link", "err", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	// No new calls; the link closes once those in progress answered (or
	// their timeout passed), then the instance deregisters.
	calls.SetDraining(true)
	stopLink()
	select {
	case <-linkDone:
	case <-time.After(cfg.CallTimeout + 5*time.Second):
	}
	stopReg()
	<-regDone
}

// fatal logs at error level and exits.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}
