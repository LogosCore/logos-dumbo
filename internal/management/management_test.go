package management

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/logoscore/logos-dumbo/internal/caller"
	"github.com/logoscore/logos-dumbo/internal/config"
	"github.com/logoscore/logos-golang-protocol/protocol"
)

func TestManifestValid(t *testing.T) {
	cfg := config.Config{ChannelID: "dumbo-1", CallTimeout: 30 * time.Second, MaxBodyBytes: 1 << 20}
	if err := protocol.ValidateManifest(Manifest(cfg)); err != nil {
		t.Fatal(err)
	}
}

func TestApply(t *testing.T) {
	c := caller.New(caller.Settings{Timeout: 30 * time.Second, MaxBodyBytes: 1 << 20})
	var level slog.LevelVar
	if err := apply(context.Background(), c, &level, map[string]any{"call_timeout": "5s", "max_body_bytes": int64(4096), "log_level": "debug"}); err != nil {
		t.Fatal(err)
	}
	if s := c.Settings(); s.Timeout != 5*time.Second || s.MaxBodyBytes != 4096 || level.Level() != slog.LevelDebug {
		t.Fatalf("%+v %v", s, level.Level())
	}
	if err := apply(context.Background(), c, &level, map[string]any{"call_timeout": "soon"}); err == nil {
		t.Fatal("a bad duration applied")
	}
}
