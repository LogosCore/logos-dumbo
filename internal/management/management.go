// Package management declares what core may manage on the channel (the
// call timeout, the answer size limit and the log level; drain and resume)
// and what it shows (call counters, the control link), bound to the
// caller. How the channel reaches core stays env-only.
package management

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/logoscore/logos-dumbo/internal/caller"
	"github.com/logoscore/logos-dumbo/internal/config"
	"github.com/logoscore/logos-golang-module-core/pkg/mgmt"
	"github.com/logoscore/logos-golang-protocol/protocol"
)

// logLevels are the log_level values, most verbose first.
var logLevels = []struct {
	value, label, description string
	level                     slog.Level
}{
	{"debug", "Debug", "Every call, plus everything below.", slog.LevelDebug},
	{"info", "Info", "Calls that failed or reached something other than a page, startup and config changes.", slog.LevelInfo},
	{"warn", "Warn", "The control link going down only.", slog.LevelWarn},
	{"error", "Error", "Errors only.", slog.LevelError},
}

func slogLevel(v any) slog.Level {
	for _, l := range logLevels {
		if l.value == v {
			return l.level
		}
	}
	return slog.LevelInfo
}

// Manifest is the management manifest; property defaults are the env's.
func Manifest(cfg config.Config) protocol.ManagementManifest {
	levels := make([]protocol.EnumOption, len(logLevels))
	for i, l := range logLevels {
		levels[i] = protocol.EnumOption{Value: l.value, Label: l.label, Description: l.description}
	}
	return protocol.ManagementManifest{
		SchemaVersion: protocol.ManagementSchemaV1,
		Properties: []protocol.PropertySpec{
			{FieldSpec: protocol.FieldSpec{
				Key: "call_timeout", Type: protocol.FieldDuration, Label: "Call timeout",
				Description: "How long a call waits for a page. A task meant to run longer belongs in the background on the page.",
				Group:       "Calls", Order: 1, Default: cfg.CallTimeout.String(), Min: "1s", Max: "10m",
			}, Apply: protocol.ApplyLive},
			{FieldSpec: protocol.FieldSpec{
				Key: "max_body_bytes", Type: protocol.FieldBytes, Label: "Max answer",
				Description: "Largest answer taken from a page; a larger one counts as not the page.",
				Group:       "Calls", Order: 2, Unit: "bytes",
				Default: cfg.MaxBodyBytes, Min: config.MinBodyBytes, Max: config.MaxBodyBytes,
			}, Apply: protocol.ApplyLive},
			{FieldSpec: protocol.FieldSpec{
				Key: "log_level", Type: protocol.FieldEnum, Label: "Log level",
				Description: "Level of the channel's own output (stderr). The Logs tab has its own level.",
				Group:       "Logging", Order: 3, Default: "info", Options: levels,
			}, Apply: protocol.ApplyLive},
		},
		Actions: []protocol.ActionSpec{
			{
				Name: "drain", Label: "Drain", Risk: protocol.RiskHigh,
				Description: "Stop calling pages: every call core asks for fails until resumed.",
				Confirm:     "Core's calls through this channel fail until it is resumed.",
				AvailableWhen: []protocol.ActionCondition{
					{Status: "draining", Op: protocol.OpEq, Value: false, Message: "Already draining"},
				},
			},
			{
				Name: "resume", Label: "Resume", Risk: protocol.RiskLow, Description: "Call pages again after a drain.",
				AvailableWhen: []protocol.ActionCondition{
					{Status: "draining", Op: protocol.OpEq, Value: true, Message: "Not draining"},
				},
			},
		},
		Status: []protocol.StatusSpec{
			{Key: "link_up", Type: protocol.FieldBool, Label: "Control link",
				Description: "Whether the control link to core is open: core reaches pages through it."},
			{Key: "calls_total", Type: protocol.FieldInt, Label: "Calls"},
			{Key: "reached_total", Type: protocol.FieldInt, Label: "Pages answered"},
			{Key: "not_minion_total", Type: protocol.FieldInt, Label: "Something else answered",
				Description: "A call reached a server, but not a page's answer (a 404, a WAF)."},
			{Key: "failed_total", Type: protocol.FieldInt, Label: "Calls failed",
				Description: "Nothing answered: DNS, refused, timeout, TLS."},
			{Key: "draining", Type: protocol.FieldBool, Label: "Draining", Alert: true},
		},
	}
}

// New is the management server for c; log_level drives level.
func New(cfg config.Config, c *caller.Caller, level *slog.LevelVar) (*mgmt.Server, error) {
	s, err := mgmt.New(Manifest(cfg), mgmt.WithSource(protocol.Source{
		Service: protocol.ServiceChannel, Instance: cfg.ChannelID,
	}))
	if err != nil {
		return nil, err
	}
	s.OnApply(func(ctx context.Context, live map[string]any) error { return apply(ctx, c, level, live) })
	s.Status(func() map[string]any {
		st := c.Stats()
		return map[string]any{"link_up": st.LinkUp, "calls_total": st.Calls, "reached_total": st.Reached,
			"not_minion_total": st.NotMinion, "failed_total": st.Failed, "draining": st.Draining}
	})
	s.Action("drain", func(_ context.Context, _ map[string]any, actor *protocol.Actor) (any, error) {
		was := c.Draining()
		c.SetDraining(true)
		slog.Info("management: drained", "by", actorName(actor))
		return map[string]any{"draining": true, "was_draining": was}, nil
	})
	s.Action("resume", func(_ context.Context, _ map[string]any, actor *protocol.Actor) (any, error) {
		was := c.Draining()
		c.SetDraining(false)
		slog.Info("management: resumed", "by", actorName(actor))
		return map[string]any{"draining": false, "was_draining": was}, nil
	})
	return s, nil
}

// apply puts live values in force.
func apply(ctx context.Context, c *caller.Caller, level *slog.LevelVar, live map[string]any) error {
	next := c.Settings()
	if v := live["call_timeout"]; v != nil {
		d, err := protocol.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("call_timeout: %w", err)
		}
		next.Timeout = d
	}
	if n, ok := protocol.AsInt64(live["max_body_bytes"]); ok {
		next.MaxBodyBytes = n
	}
	lvl := slogLevel(live["log_level"])
	if next == c.Settings() && level.Level() == lvl {
		return nil
	}
	c.SetSettings(next)
	level.Set(lvl)
	slog.Info("management: config applied", "by", actorName(mgmt.ActorFromContext(ctx)),
		"call_timeout", next.Timeout.String(), "max_body_bytes", next.MaxBodyBytes, "log_level", lvl.String())
	return nil
}

// actorName says who changed something, for the log.
func actorName(a *protocol.Actor) string {
	switch {
	case a == nil:
		return "core"
	case a.Name != "":
		return fmt.Sprintf("%s %s (%s)", a.Type, a.Name, a.ID)
	default:
		return a.Type + " " + a.ID
	}
}
