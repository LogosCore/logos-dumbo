package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain gives every test the broker password, which has no default.
func TestMain(m *testing.M) {
	_ = os.Setenv("RABBITMQ_PASSWORD", "secret")
	os.Exit(m.Run())
}

func TestDefaults(t *testing.T) {
	for _, k := range []string{"CHANNEL_ID", "LOGOS_CORE_BASE_URL", "CALL_TIMEOUT", "MAX_BODY_BYTES", "LOG_FORMAT",
		"RABBITMQ_USER", "RABBITMQ_HOST", "RABBITMQ_PORT", "RABBITMQ_VHOST"} {
		t.Setenv(k, "")
	}
	cfg, err := LoadFromEnv("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChannelID != "dumbo-main" || cfg.CoreBaseURL != "http://localhost:8002" || cfg.CallTimeout != 30*time.Second ||
		cfg.MaxBodyBytes != 1<<20 || cfg.LogFormat != "json" || cfg.RabbitMQURL != "amqp://logos:secret@localhost:5672//" {
		t.Fatalf("defaults: %+v", cfg)
	}
}

// The broker password has no default: without it the channel does not start.
func TestPasswordRequired(t *testing.T) {
	t.Setenv("RABBITMQ_PASSWORD", "")
	if _, err := LoadFromEnv(""); err == nil {
		t.Fatal("no RABBITMQ_PASSWORD: taken")
	}
}

func TestValues(t *testing.T) {
	t.Setenv("LOGOS_CORE_BASE_URL", "https://core.example:8443/")
	t.Setenv("CALL_TIMEOUT", "45s")
	t.Setenv("MAX_BODY_BYTES", "2097152")
	t.Setenv("LOG_FORMAT", "TEXT")
	t.Setenv("RABBITMQ_VHOST", "logos")
	cfg, err := LoadFromEnv("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CoreBaseURL != "https://core.example:8443" || cfg.CallTimeout != 45*time.Second || cfg.MaxBodyBytes != 2<<20 ||
		cfg.LogFormat != "text" || !strings.HasSuffix(cfg.RabbitMQURL, "/logos") {
		t.Fatalf("values: %+v", cfg)
	}
}

func TestRefused(t *testing.T) {
	for k, v := range map[string]string{
		"LOGOS_CORE_BASE_URL": "core:8002",
		"CALL_TIMEOUT":        "500ms",
		"MAX_BODY_BYTES":      "10",
		"LOG_FORMAT":          "xml",
	} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, err := LoadFromEnv(""); err == nil {
				t.Fatalf("%s=%s taken", k, v)
			}
		})
	}
	t.Setenv("CALL_TIMEOUT", "11m")
	if _, err := LoadFromEnv(""); err == nil {
		t.Fatal("CALL_TIMEOUT=11m taken")
	}
}
