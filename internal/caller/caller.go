// Package caller makes the calls core asks for over the control link: it
// delivers a sealed message to a page and answers with what came back
// (docs: contracts/channel-core-connections/, bind-call minions).
//
// Wire format between the channel and a page, both ways: the request body
// (and a page's answer) is the id the message is sent under, 16 raw bytes,
// followed by the sealed frame, application/octet-stream. The channel never
// reads the frame: core opens it.
package caller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/logoscore/logos-dumbo/internal/connection"
	"github.com/logoscore/logos-golang-channel-core/v3/pkg/linkclient"
	"github.com/logoscore/logos-golang-protocol/protocol"
)

// DefaultUserAgent is sent when a target does not say one: a common
// browser's, as a page's visitors send.
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36"

// maxTransports bounds the transports kept, one per proxy and TLS setting.
const maxTransports = 64

// Settings are what core can change live.
type Settings struct {
	// Timeout bounds a call: core's deadline, when shorter, wins.
	Timeout time.Duration
	// MaxBodyBytes bounds what a page answers.
	MaxBodyBytes int64
}

// Stats are the caller's counters.
type Stats struct {
	Calls, Reached, Failed, NotMinion int64
	LinkUp, Draining                  bool
}

// Caller makes calls.
type Caller struct {
	settings atomic.Pointer[Settings]
	draining atomic.Bool
	linkUp   atomic.Bool

	calls, reached, failed, notMinion atomic.Int64

	mu         sync.Mutex
	transports map[string]*http.Transport
}

// New is a caller with s.
func New(s Settings) *Caller {
	c := &Caller{transports: map[string]*http.Transport{}}
	c.SetSettings(s)
	return c
}

// SetSettings changes the settings for calls from now on.
func (c *Caller) SetSettings(s Settings) { c.settings.Store(&s) }

// Settings are the settings in force.
func (c *Caller) Settings() Settings { return *c.settings.Load() }

// SetDraining stops (true) or resumes calls: while draining every call
// fails at once.
func (c *Caller) SetDraining(v bool) { c.draining.Store(v) }

// Draining reports whether calls are refused.
func (c *Caller) Draining() bool { return c.draining.Load() }

// SetLinkUp records whether the control link is open (linkclient's
// OnState).
func (c *Caller) SetLinkUp(up bool) { c.linkUp.Store(up) }

// Stats are the counters now.
func (c *Caller) Stats() Stats {
	return Stats{Calls: c.calls.Load(), Reached: c.reached.Load(), Failed: c.failed.Load(),
		NotMinion: c.notMinion.Load(), LinkUp: c.linkUp.Load(), Draining: c.draining.Load()}
}

// Call makes one call: the control link's handler.
func (c *Caller) Call(ctx context.Context, call linkclient.Call) linkclient.Answer {
	c.calls.Add(1)
	start := time.Now()
	a := c.call(ctx, call)
	outcome, level := "reached", slog.LevelDebug
	switch {
	case a.Failure != nil:
		c.failed.Add(1)
		outcome, level = protocol.LinkFailName(a.Failure.Kind), slog.LevelInfo
	case a.ID == nil:
		c.notMinion.Add(1)
		outcome, level = "not_the_minion", slog.LevelInfo
	default:
		c.reached.Add(1)
	}
	args := []any{"request", call.Request, "url", call.Values["url"], "outcome", outcome,
		"duration_ms", time.Since(start).Milliseconds()}
	if a.Status != 0 {
		args = append(args, "status", a.Status)
	}
	if a.Failure != nil {
		args = append(args, "err", a.Failure.Msg)
	}
	slog.Log(ctx, level, "call", args...)
	return a
}

func (c *Caller) call(ctx context.Context, call linkclient.Call) linkclient.Answer {
	if c.Draining() {
		return linkclient.Fail(protocol.LinkFailOther, "the channel is draining")
	}
	t, err := readTarget(call.Values)
	if err != nil {
		return linkclient.Fail(protocol.LinkFailOther, "%s", err)
	}
	s := c.Settings()
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	body := make([]byte, 0, 16+len(call.Data))
	body = append(append(body, call.ID[:]...), call.Data...)
	req, err := http.NewRequestWithContext(ctx, t.method, t.url.String(), strings.NewReader(string(body)))
	if err != nil {
		return linkclient.Fail(protocol.LinkFailOther, "request: %s", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", t.userAgent)
	if t.hostHeader != "" {
		req.Host = t.hostHeader
	}
	// The address the call went to: the minion's external address.
	var peer string
	if t.proxy == nil {
		req = req.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { peer = remoteIP(info.Conn) },
		}))
	} else {
		peer = literalIP(t.url.Hostname())
	}
	resp, err := c.transport(t).RoundTrip(req)
	if err != nil {
		return failure(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(io.LimitReader(resp.Body, s.MaxBodyBytes+1))
	if err != nil {
		return failure(ctx, err)
	}
	// The page's answer: a 200 with an id and a frame. Anything else
	// answered, but not as the page: core shows what.
	if resp.StatusCode == http.StatusOK && int64(len(got)) <= s.MaxBodyBytes && len(got) >= 16+protocol.MinFrameSize &&
		resp.Header.Get("Content-Type") == "application/octet-stream" {
		var id [16]byte
		copy(id[:], got)
		return linkclient.Answer{ID: &id, Data: got[16:], Status: resp.StatusCode, Peer: peer}
	}
	return linkclient.Answer{Status: resp.StatusCode, Excerpt: protocol.Excerpt(got), Peer: peer}
}

// remoteIP is the IP address c is connected to; "" when it has none.
func remoteIP(c net.Conn) string {
	if c == nil {
		return ""
	}
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// literalIP is host when it is an IP address: through a proxy the channel
// knows where the call went only then (a name, the proxy resolves; Go's
// SOCKS5 client sends names too, socks5 as socks5h). "" for a name.
func literalIP(host string) string {
	if a, err := netip.ParseAddr(host); err == nil {
		return a.Unmap().String()
	}
	return ""
}

// target is one call's values.
type target struct {
	method                string
	url                   *url.URL
	hostHeader, userAgent string
	proxy                 *url.URL
	tlsVerify             bool
	headers               map[string]string
}

// readTarget reads a call's values: core checked them against the
// connection schema, but they come over the wire all the same.
func readTarget(v map[string]any) (target, error) {
	str := func(k string) string { s, _ := v[k].(string); return s }
	t := target{method: str("method"), hostHeader: str("host_header"), userAgent: str("user_agent")}
	if t.method == "" {
		t.method = http.MethodPost
	}
	if !slices.Contains(connection.Methods, t.method) {
		return t, fmt.Errorf("method %q", t.method)
	}
	if t.userAgent == "" {
		t.userAgent = DefaultUserAgent
	}
	raw := str("url")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return t, fmt.Errorf("url %q: an http(s) URL", raw)
	}
	t.url = u
	if p := str("proxy"); p != "" {
		if t.proxy, err = url.Parse(p); err != nil || t.proxy.Host == "" {
			return t, fmt.Errorf("proxy %q", p)
		}
	}
	t.tlsVerify, _ = v["tls_verify"].(bool)
	if hs, ok := v["headers"].(map[string]any); ok {
		t.headers = map[string]string{}
		for k, val := range hs {
			if s, ok := val.(string); ok {
				t.headers[k] = s
			}
		}
	}
	return t, nil
}

// transport is the transport for t's proxy and TLS setting, kept for the
// calls after.
func (c *Caller) transport(t target) *http.Transport {
	key := fmt.Sprintf("%t|", t.tlsVerify)
	if t.proxy != nil {
		key += t.proxy.String()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if tr, ok := c.transports[key]; ok {
		return tr
	}
	if len(c.transports) >= maxTransports {
		for _, tr := range c.transports {
			tr.CloseIdleConnections()
		}
		c.transports = map[string]*http.Transport{}
	}
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 4,
		ForceAttemptHTTP2:   true,
		// A page's server often has a certificate nothing trusts: the
		// target says whether to check it.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !t.tlsVerify},
	}
	if t.proxy != nil {
		tr.Proxy = http.ProxyURL(t.proxy)
	}
	c.transports[key] = tr
	return tr
}

// failure is the answer for a call that reached nothing.
func failure(ctx context.Context, err error) linkclient.Answer {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalid x509.CertificateInvalidError
	var recordErr tls.RecordHeaderError
	var netErr net.Error
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || (errors.As(err, &netErr) && netErr.Timeout()):
		return linkclient.Fail(protocol.LinkFailTimeout, "%s", err)
	case errors.As(err, &certErr), errors.As(err, &unknownAuth), errors.As(err, &hostErr), errors.As(err, &invalid),
		errors.As(err, &recordErr):
		return linkclient.Fail(protocol.LinkFailTLS, "%s", err)
	case errors.As(err, &dnsErr), errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH), errors.As(err, &opErr) && opErr.Op == "dial":
		return linkclient.Fail(protocol.LinkFailUnreachable, "%s", err)
	}
	return linkclient.Fail(protocol.LinkFailOther, "%s", err)
}
