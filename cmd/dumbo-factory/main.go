// Command dumbo-factory is Dumbo: a factory that builds PHP pages, bind-call
// minions core reaches through the Dumbo channel (build.go, page.php).
package main

import (
	"context"
	_ "embed"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/logoscore/logos-golang-factory-core/pkg/factory"
	"github.com/logoscore/logos-golang-module-core/pkg/lifecycle"
	"github.com/logoscore/logos-golang-module-core/pkg/logging"
	"github.com/logoscore/logos-golang-module-core/pkg/logstream"
	"github.com/logoscore/logos-golang-module-core/pkg/module"
)

// version is reported at registration and goes into the pages' hello; set
// with -ldflags "-X main.version=...".
var version = "dev"

// icon is Dumbo's icon in core's UI, sent at registration: a baby elephant,
// sitting (the elephant is PHP's), traced from a line drawing, the Dumbo
// channel's own. The app paints an icon in one colour, so for dark themes
// it is the filled figure with the drawing's lines cut out of it.
//
//go:embed icon.svg
var icon string

// iconLight is the same drawing for light themes, where the filled figure
// would read as a negative: its lines are the shape.
//
//go:embed icon-light.svg
var iconLight string

func main() {
	instance := env("FACTORY_ID", "dumbo-factory-local")

	// Logs go to stderr (LOG_FORMAT) at the managed log_level, and to core's
	// live log tail through the hub.
	var level slog.LevelVar
	logs := logstream.New(instance)
	logger, err := logging.New(os.Getenv("LOG_FORMAT"), os.Stderr, &level, logs)
	if err != nil {
		log.Fatalf("logging: %v", err)
	}
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("logos-dumbo-factory starting", "instance", instance, "version", version)
	mgmtSrv, err := newManagement(&level)
	if err != nil {
		fatal("management", "err", err)
	}
	broker, err := amqpURL()
	if err != nil {
		fatal("config", "err", err)
	}
	schema := buildSchema
	err = factory.Run(ctx, factory.Config{
		Config: module.Config{
			AMQPURL: broker,
			Identity: lifecycle.Identity{
				ModuleName:  "dumbo",
				Instance:    instance,
				Version:     version,
				Description: "Dumbo: builds PHP pages, bind-call minions core reaches through the Dumbo channel",
				Icon:        icon,
				IconLight:   iconLight,
			},
			Management: mgmtSrv,
			Logs:       logs,
		},
		Build:               &schema,
		Builder:             &dumbo{version: version},
		MaxConcurrentBuilds: 4,
		// Builds fetch their files from core and upload the artifact to it:
		// core's own address, or its front (logos-nginx). Over HTTPS to a
		// self-signed core, module-core's LOGOS_CORE_* TLS settings.
		CoreURL: env("LOGOS_CORE_BASE_URL", "http://localhost:8002"),
	})
	if err != nil {
		fatal("control plane failed", "err", err)
	}
	slog.Info("bye")
}

// fatal logs at error level and exits.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// amqpURL is core's broker's: its user is logos-core's
// RABBITMQ_DEFAULT_USER; its password (RABBITMQ_DEFAULT_PASS) has no
// default that could fit.
func amqpURL() (string, error) {
	password := os.Getenv("RABBITMQ_PASSWORD")
	if password == "" {
		return "", errors.New("RABBITMQ_PASSWORD is required: core's broker password (logos-core's RABBITMQ_DEFAULT_PASS)")
	}
	u := url.URL{
		Scheme: "amqp",
		User:   url.UserPassword(env("RABBITMQ_USER", "logos"), password),
		Host:   net.JoinHostPort(env("RABBITMQ_HOST", "localhost"), env("RABBITMQ_PORT", "5672")),
		// amqp091 takes the vhost from the path minus its leading "/".
		Path: "/" + env("RABBITMQ_VHOST", "/"),
	}
	return u.String(), nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
