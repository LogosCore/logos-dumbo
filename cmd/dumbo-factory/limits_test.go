package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logoscore/logos-golang-protocol/protocol"
)

// A call whose at is far from the page's clock, or that says none, is
// refused as stale: nothing in it runs, and the err says the page's time.
func TestPageStaleCall(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	hello := protocol.TaskItem{T: protocol.ItemTask, Task: taskID(), Cmd: opHello}
	for name, at := range map[string]uint64{
		"an hour old": uint64(time.Now().Add(-time.Hour).Unix()),
		"none":        0,
	} {
		status, msg := callAt(t, url, "POST", session, testKeys, at, hello)
		if status != http.StatusOK {
			t.Fatalf("%s: HTTP %d", name, status)
		}
		items := byType(t, msg)
		var e protocol.ErrItem
		if len(items[protocol.ItemErr]) != 1 || protocol.UnmarshalCBOR(items[protocol.ItemErr][0], &e) != nil ||
			e.Code != protocol.ErrItemStale || !strings.Contains(e.Msg, "the page's time is") {
			t.Fatalf("%s: %+v", name, e)
		}
		if len(items[protocol.ItemResult]) != 0 || len(items[protocol.ItemAck]) != 0 || len(items[protocol.ItemGetTasks]) != 0 {
			t.Fatalf("%s: the call ran: %v", name, items)
		}
	}
	// On time: it runs.
	_, msg := call(t, url, "POST", session, testKeys, hello)
	if len(byType(t, msg)[protocol.ItemResult]) != 1 {
		t.Fatal("a call on time did not run")
	}
}

// An err core sends about the page's ask for an upload's chunk carries the
// ask's ctx: the page removes the partial file it names.
func TestPageErrCleansUp(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	dir := t.TempDir()
	body := make([]byte, 5000)
	_, _ = rand.Read(body)
	sum := sha256.Sum256(body)
	task, xfer := taskID(), taskID()
	offer := protocol.FileOffer{Xfer: xfer, Name: "up.bin", Size: uint64(len(body)), SHA256: sum[:]}
	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: task, Cmd: opUpload, Args: protocol.Args{1: offer, 2: dir, 3: false}})
	var ask protocol.AckItem
	for _, raw := range byType(t, msg)[protocol.ItemAck] {
		if protocol.UnmarshalCBOR(raw, &ask) == nil && ask.Xfer != nil {
			break
		}
	}
	if ask.Xfer == nil || len(ask.Ctx) == 0 {
		t.Fatalf("no ask: %v", byType(t, msg))
	}
	total := uint64(len(body))
	_, _ = call(t, url, "POST", session, testKeys, protocol.ChunkItem{T: protocol.ItemChunk, Xfer: xfer, Off: 0, Data: body[:1000],
		Total: &total, Task: task, Ctx: ask.Ctx})
	parts, _ := filepath.Glob(filepath.Join(dir, ".*.part"))
	if len(parts) != 1 {
		t.Fatalf("parts %v", parts)
	}
	item := 0
	_, _ = call(t, url, "POST", session, testKeys, protocol.ErrItem{T: protocol.ItemErr, Code: protocol.ErrItemUnknownTask,
		Msg: "the transfer's task ended", Item: &item, Ctx: ask.Ctx})
	if _, err := os.Stat(parts[0]); !os.IsNotExist(err) {
		t.Fatalf("the partial file is still there: %v", err)
	}
}

// A connection that carries little (its channel's max_message) gets a page
// that answers within it: small chunks.
func TestPageWithinTheConnection(t *testing.T) {
	req := request(t, "POST")
	req.Connections[0].MaxMessage = 4096
	page, conn, err := render(req, "test")
	if err != nil {
		t.Fatal(err)
	}
	chunk, walk, entries, piece := conn.sizes()
	if chunk != 4096*2/3 || walk != 3276 || entries != 27 || piece != 10 {
		t.Fatalf("sizes %d %d %d %d", chunk, walk, entries, piece)
	}
	if !strings.Contains(page, "const CHUNK = 2730;") || strings.Contains(page, "__CHUNK__") {
		t.Fatal("the page's chunk is not the connection's")
	}
	url := startPage(t, req)
	var session [16]byte
	copy(session[:], taskID())
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	body := bytes.Repeat([]byte("x"), 10000)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: taskID(), Cmd: opDownload, Args: protocol.Args{1: path}})
	var c protocol.ChunkItem
	chunks := byType(t, msg)[protocol.ItemChunk]
	if len(chunks) != 1 || protocol.UnmarshalCBOR(chunks[0], &c) != nil || len(c.Data) != chunk {
		t.Fatalf("a chunk of %d bytes, not %d", len(c.Data), chunk)
	}
	// The default: the page's usual sizes.
	if c, w, e, p := (pageConn{limit: protocol.MaxMinionMessageSize}).sizes(); c != protocol.MaxChunkData || w != 600000 || e != 5000 || p != 500 {
		t.Fatalf("default sizes %d %d %d %d", c, w, e, p)
	}
}
