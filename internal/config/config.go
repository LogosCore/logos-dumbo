// Package config reads the channel's settings from the environment (and a
// .env file as fallback).
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is what the process runs with.
type Config struct {
	// ChannelID is the instance id core knows the process by.
	ChannelID string
	// CoreBaseURL is core's base URL: the control link opens under it.
	CoreBaseURL string
	// CallTimeout bounds each call to a page (CALL_TIMEOUT, 30s).
	CallTimeout time.Duration
	// MaxBodyBytes bounds what a page answers (MAX_BODY_BYTES, 1 MiB).
	MaxBodyBytes int64
	// RabbitMQURL is the AMQP URL for registering with core.
	RabbitMQURL string
	// LogFormat is the stderr log format: "json" (default) or "text".
	LogFormat string
}

// Bounds of MAX_BODY_BYTES: an id and the smallest sealed frame, and a
// ceiling far above any minion message.
const (
	MinBodyBytes = 16 + 64
	MaxBodyBytes = 64 << 20
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// LoadFromEnv reads the config; envFile, when it exists, fills in what the
// environment does not set.
func LoadFromEnv(envFile string) (Config, error) {
	if envFile != "" {
		_ = godotenv.Load(envFile)
	}
	cfg := Config{
		ChannelID:   envOr("CHANNEL_ID", "dumbo-main"),
		CoreBaseURL: strings.TrimRight(envOr("LOGOS_CORE_BASE_URL", "http://localhost:8002"), "/"),
	}
	if u, err := url.Parse(cfg.CoreBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("LOGOS_CORE_BASE_URL must be http(s)://host[:port], got %q", cfg.CoreBaseURL)
	}
	d, err := time.ParseDuration(envOr("CALL_TIMEOUT", "30s"))
	if err != nil || d < time.Second || d > 10*time.Minute {
		return Config{}, fmt.Errorf("CALL_TIMEOUT must be a duration from 1s to 10m (e.g. 30s)")
	}
	cfg.CallTimeout = d
	n, err := strconv.ParseInt(envOr("MAX_BODY_BYTES", "1048576"), 10, 64)
	if err != nil || n < MinBodyBytes || n > MaxBodyBytes {
		return Config{}, fmt.Errorf("MAX_BODY_BYTES must be an integer from %d to %d", MinBodyBytes, MaxBodyBytes)
	}
	cfg.MaxBodyBytes = n
	vhost := envOr("RABBITMQ_VHOST", "/")
	cfg.RabbitMQURL = (&url.URL{
		Scheme: "amqp",
		User:   url.UserPassword(envOr("RABBITMQ_USER", "guest"), envOr("RABBITMQ_PASSWORD", "guest")),
		Host:   net.JoinHostPort(envOr("RABBITMQ_HOST", "localhost"), envOr("RABBITMQ_PORT", "5672")),
		// amqp091 takes the vhost from the path minus its leading "/".
		Path: "/" + vhost,
	}).String()
	cfg.LogFormat = strings.ToLower(envOr("LOG_FORMAT", "json"))
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return Config{}, fmt.Errorf("LOG_FORMAT must be json or text")
	}
	return cfg, nil
}
