package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/logoscore/logos-golang-protocol/protocol"
)

// Streams with the page under real PHP, the test playing core: it holds a
// stream (no downloader, no uploader's bytes), then lets it go with an ack
// the page did not ask for, carrying the stream's ctx as core keeps it.

func TestPageStreamDownload(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	dir := t.TempDir()
	body := randomFile(t, dir, "big.iso", protocol.MaxChunkData+4000)
	path := filepath.Join(dir, "big.iso")
	task := taskID()

	// The announcement: a first chunk without data.
	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: task, Cmd: opStreamDownload, Args: protocol.Args{1: path}})
	var a protocol.ChunkItem
	one(t, msg, protocol.ItemChunk, &a)
	if err := a.Validate(); err != nil || a.Off != 0 || len(a.Data) != 0 || a.Total == nil || *a.Total != uint64(len(body)) || a.Name != path || len(a.Ctx) == 0 {
		t.Fatalf("announcement %+v: %v", a, err)
	}
	xfer, ctx := a.Xfer, a.Ctx
	ack := func(next uint64, hold bool) *protocol.MinionMessage {
		_, msg := call(t, url, "POST", session, testKeys, protocol.AckItem{T: protocol.ItemAck, Xfer: xfer, Next: &next, Hold: hold, Ctx: ctx})
		return msg
	}
	// Held: the page sends nothing of it.
	if items := byType(t, ack(0, true)); len(items[protocol.ItemChunk]) != 0 || len(items[protocol.ItemResult]) != 0 {
		t.Fatalf("held, the page sent %v", items)
	}
	// A downloader came: the chunks from where core says, the first a
	// first chunk again.
	var got []byte
	for next := uint64(0); next < uint64(len(body)); next = uint64(len(got)) {
		var c protocol.ChunkItem
		one(t, ack(next, false), protocol.ItemChunk, &c)
		if err := c.Validate(); err != nil || c.Off != next || (next == 0) != (c.Total != nil) || !bytes.Equal(c.Ctx, ctx) {
			t.Fatalf("chunk at %d: %+v %v", next, c.Off, err)
		}
		got = append(got, c.Data...)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("the bytes streamed are not the file's")
	}
	// The downloader has every byte: the task's end.
	r := result(t, ack(uint64(len(body)), false))
	named, _ := protocol.NamedResultData(protocol.ResultFileStream, r.Data)
	f, err := protocol.ReadFileStream(0, named)
	sum := sha256.Sum256(body)
	if err != nil || !*r.OK || f.Path != path || f.Size != uint64(len(body)) || !bytes.Equal(f.SHA256, sum[:]) {
		t.Fatalf("result %+v: %+v %v", r, f, err)
	}
}

func TestPageStreamUpload(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	dir := t.TempDir()
	body := make([]byte, protocol.MaxChunkData+5000)
	_, _ = rand.Read(body)
	sum := sha256.Sum256(body)
	task, xfer := taskID(), taskID()
	offer := protocol.FileOffer{Xfer: xfer, Name: "up.iso", Stream: true}
	ask := func(msg *protocol.MinionMessage) *protocol.AckItem {
		t.Helper()
		for _, raw := range byType(t, msg)[protocol.ItemAck] {
			var a protocol.AckItem
			if protocol.UnmarshalCBOR(raw, &a) == nil && a.Xfer != nil {
				return &a
			}
		}
		return nil
	}

	// Ready: the first ask.
	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: task, Cmd: opStreamUpload, Args: protocol.Args{1: offer, 2: dir}})
	a := ask(msg)
	if a == nil || *a.Next != 0 || len(a.Ctx) == 0 {
		t.Fatalf("first ask %+v", a)
	}
	ctx := a.Ctx
	// Core has no bytes yet: hold, and the page asks no more.
	zero := uint64(0)
	_, msg = call(t, url, "POST", session, testKeys, protocol.AckItem{T: protocol.ItemAck, Xfer: xfer, Next: &zero, Hold: true, Ctx: ctx})
	if ask(msg) != nil {
		t.Fatal("asked while held")
	}
	// The uploader's bytes came: core's ack, unasked, and the page asks.
	_, msg = call(t, url, "POST", session, testKeys, protocol.AckItem{T: protocol.ItemAck, Xfer: xfer, Next: &zero, Ctx: ctx})
	if a = ask(msg); a == nil || *a.Next != 0 {
		t.Fatalf("no ask once bytes came: %+v", a)
	}
	total := uint64(len(body))
	first := protocol.ChunkItem{T: protocol.ItemChunk, Xfer: xfer, Data: body[:protocol.MaxChunkData], Total: &total, Task: task, Name: "up.iso", Ctx: ctx}
	_, msg = call(t, url, "POST", session, testKeys, first)
	if a = ask(msg); a == nil || *a.Next != protocol.MaxChunkData {
		t.Fatalf("after the first chunk %+v", a)
	}
	last := protocol.ChunkItem{T: protocol.ItemChunk, Xfer: xfer, Off: protocol.MaxChunkData, Data: body[protocol.MaxChunkData:], SHA256: sum[:], Ctx: ctx}
	_, msg = call(t, url, "POST", session, testKeys, last)
	if a = ask(msg); a == nil || *a.Next != total {
		t.Fatalf("the last ask %+v", a)
	}
	r := result(t, msg)
	named, _ := protocol.NamedResultData(protocol.ResultFileUpload, r.Data)
	f, err := protocol.ReadFileUpload(0, named)
	dest := filepath.Join(dir, "up.iso")
	if err != nil || !*r.OK || f.Path != dest || f.Size != total || !bytes.Equal(f.SHA256, sum[:]) {
		t.Fatalf("result %+v: %+v %v", r, f, err)
	}
	if written, _ := os.ReadFile(dest); !bytes.Equal(written, body) {
		t.Fatal("the file written is not the one streamed")
	}
	// Stopped part way: the cancel carries the transfer's ctx (core's
	// fallback), and the page removes what came and ends the task.
	stopped, sx := taskID(), taskID()
	_, msg = call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: stopped, Cmd: opStreamUpload,
		Args: protocol.Args{1: protocol.FileOffer{Xfer: sx, Name: "half.iso", Stream: true}, 2: dir}})
	a = ask(msg)
	if a == nil {
		t.Fatal("no first ask")
	}
	sctx := a.Ctx
	_, _ = call(t, url, "POST", session, testKeys, protocol.ChunkItem{T: protocol.ItemChunk, Xfer: sx, Data: body[:1000], Total: &total,
		Task: stopped, Name: "half.iso", Ctx: sctx})
	parts, _ := filepath.Glob(filepath.Join(dir, ".half.iso.*.part"))
	if len(parts) != 1 {
		t.Fatalf("partial files %v", parts)
	}
	_, msg = call(t, url, "POST", session, testKeys, protocol.CancelItem{T: protocol.ItemCancel, Task: stopped, Ctx: sctx})
	if r := result(t, msg); !r.Done || *r.OK || r.Err != "stopped: 1000 bytes had come; the partial file is removed" {
		t.Fatalf("stopped: %+v", r)
	}
	if _, err := os.Stat(parts[0]); !os.IsNotExist(err) {
		t.Fatal("the partial file is still there")
	}
	// A plain file where a stream is wanted: refused.
	_, msg = call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: taskID(), Cmd: opStreamUpload,
		Args: protocol.Args{1: protocol.FileOffer{Xfer: taskID(), Name: "x", Size: 1, SHA256: make([]byte, 32)}, 2: dir}})
	if r := result(t, msg); *r.OK {
		t.Fatalf("a file as a stream: %+v", r)
	}
}
