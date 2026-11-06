package caller

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/logoscore/logos-golang-channel-core/v3/pkg/linkclient"
	"github.com/logoscore/logos-golang-protocol/protocol"
)

var frame = bytes.Repeat([]byte{0xab}, protocol.MinFrameSize)

// page answers as a page does: the id it was called under and a frame.
func page(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPut || r.Header.Get("X-Site") != "1" || r.Host != "cdn.example" ||
			r.UserAgent() != "ua" || len(body) != 16+len(frame) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(append(body[:16:16], bytes.Repeat([]byte{0xcd}, protocol.MinFrameSize)...))
	})
}

func newCaller() *Caller {
	return New(Settings{Timeout: 2 * time.Second, MaxBodyBytes: 1 << 20})
}

func call(url string, extra map[string]any) linkclient.Call {
	v := map[string]any{"method": "PUT", "url": url, "host_header": "cdn.example", "user_agent": "ua",
		"headers": map[string]any{"X-Site": "1"}}
	for k, x := range extra {
		v[k] = x
	}
	return linkclient.Call{Request: 1, Values: v, ID: [16]byte{1, 2, 3}, Data: frame}
}

func TestReached(t *testing.T) {
	for name, srv := range map[string]*httptest.Server{"http": httptest.NewServer(page(t)), "https": httptest.NewTLSServer(page(t))} {
		a := newCaller().Call(context.Background(), call(srv.URL+"/a.php", nil))
		if a.Failure != nil || a.ID == nil || *a.ID != [16]byte{1, 2, 3} || len(a.Data) != protocol.MinFrameSize || a.Status != 200 {
			t.Errorf("%s: %+v", name, a)
		}
		srv.Close()
	}
}

// The answer says the address the call went to: the one connected to
// (twice: a kept connection too), or, through a proxy, the target's when
// it is an IP address; a name the proxy resolves says none.
func TestPeer(t *testing.T) {
	srv := httptest.NewServer(page(t))
	defer srv.Close()
	c := newCaller()
	for i := range 2 {
		if a := c.Call(context.Background(), call(srv.URL+"/a.php", nil)); a.ID == nil || a.Peer != "127.0.0.1" {
			t.Fatalf("direct, call %d: %+v", i+1, a)
		}
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RequestURI = ""
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()
	// No host header: through a plain HTTP proxy it would name the host the
	// proxy dials. The page's 404 still says where the call went.
	via := map[string]any{"proxy": proxy.URL, "host_header": ""}
	if a := c.Call(context.Background(), call(srv.URL+"/a.php", via)); a.Failure != nil || a.Status != 404 || a.Peer != "127.0.0.1" {
		t.Fatalf("an IP through a proxy: %+v", a)
	}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	if a := c.Call(context.Background(), call("http://localhost:"+port+"/a.php", via)); a.Failure != nil || a.Status != 404 || a.Peer != "" {
		t.Fatalf("a name through a proxy: %+v", a)
	}
}

func TestNotTheMinion(t *testing.T) {
	srv := httptest.NewServer(page(t))
	defer srv.Close()
	// Another method: the page's 404.
	a := newCaller().Call(context.Background(), call(srv.URL, map[string]any{"method": "POST"}))
	if a.Failure != nil || a.ID != nil || a.Status != 404 || !bytes.Contains(a.Excerpt, []byte("not found")) {
		t.Fatalf("%+v", a)
	}
	// A 200 that is not a page's answer (an HTML page).
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("<html>"), 100))
	}))
	defer html.Close()
	a = newCaller().Call(context.Background(), call(html.URL, nil))
	if a.ID != nil || a.Status != 200 || len(a.Excerpt) != protocol.MaxLinkExcerpt {
		t.Fatalf("html: %+v", a)
	}
}

func TestFailures(t *testing.T) {
	// A port nothing listens on.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	if a := newCaller().Call(context.Background(), call("http://"+addr, nil)); a.Failure == nil || a.Failure.Kind != protocol.LinkFailUnreachable {
		t.Errorf("refused: %+v", a)
	}
	// A certificate nothing trusts, checked.
	tlsSrv := httptest.NewTLSServer(page(t))
	defer tlsSrv.Close()
	if a := newCaller().Call(context.Background(), call(tlsSrv.URL, map[string]any{"tls_verify": true})); a.Failure == nil || a.Failure.Kind != protocol.LinkFailTLS {
		t.Errorf("tls: %+v", a)
	}
	// A page that does not answer in time.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer slow.Close()
	c := New(Settings{Timeout: 100 * time.Millisecond, MaxBodyBytes: 1 << 20})
	if a := c.Call(context.Background(), call(slow.URL, nil)); a.Failure == nil || a.Failure.Kind != protocol.LinkFailTimeout {
		t.Errorf("timeout: %+v", a)
	}
	// Values that make no request.
	for _, v := range []map[string]any{{"url": "ftp://x"}, {"method": "DELETE"}, {"proxy": "::"}} {
		if a := newCaller().Call(context.Background(), call(tlsSrv.URL, v)); a.Failure == nil || a.Failure.Kind != protocol.LinkFailOther {
			t.Errorf("%v: %+v", v, a)
		}
	}
	// Draining.
	c.SetDraining(true)
	if a := c.Call(context.Background(), call(tlsSrv.URL, nil)); a.Failure == nil {
		t.Errorf("draining: %+v", a)
	}
	if st := c.Stats(); st.Calls != 2 || st.Failed != 2 || !st.Draining {
		t.Errorf("stats %+v", st)
	}
}
