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

// File transfers with the page under real PHP, the test playing core: it
// echoes the transfer's ctx on every ack and chunk, as core does, and the
// page keeps nothing between calls.

// one is the single item of type kind in msg.
func one(t *testing.T, msg *protocol.MinionMessage, kind int, v any) {
	t.Helper()
	got := byType(t, msg)[kind]
	if len(got) != 1 {
		t.Fatalf("%d items of type %d in %v", len(got), kind, byType(t, msg))
	}
	if err := protocol.UnmarshalCBOR(got[0], v); err != nil {
		t.Fatal(err)
	}
}

func result(t *testing.T, msg *protocol.MinionMessage) protocol.ResultItem {
	t.Helper()
	var r protocol.ResultItem
	one(t, msg, protocol.ItemResult, &r)
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func randomFile(t *testing.T, dir, name string, n int) []byte {
	t.Helper()
	body := make([]byte, n)
	_, _ = rand.Read(body)
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestPageDownload(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	dir := t.TempDir()
	body := randomFile(t, dir, "big.bin", 2*protocol.MaxChunkData+1000)
	path := filepath.Join(dir, "big.bin")
	task := taskID()

	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: task, Cmd: opDownload, Args: protocol.Args{1: path}})
	var c protocol.ChunkItem
	one(t, msg, protocol.ItemChunk, &c)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Off != 0 || c.Total == nil || *c.Total != uint64(len(body)) || !bytes.Equal(c.Task, task) || c.Name != path || len(c.Ctx) == 0 {
		t.Fatalf("first chunk: off %d, total %v, name %q, ctx %d bytes", c.Off, c.Total, c.Name, len(c.Ctx))
	}
	got := append([]byte{}, c.Data...)
	xfer, ctx := c.Xfer, c.Ctx
	// Core acks each chunk, echoing its ctx; the page sends the next, from
	// where the ack says. The ack after the last ends the task.
	for calls := 0; ; calls++ {
		next := uint64(len(got))
		_, msg = call(t, url, "POST", session, testKeys, protocol.AckItem{T: protocol.ItemAck, Xfer: xfer, Next: &next, Ctx: ctx})
		if next == uint64(len(body)) {
			break
		}
		var c protocol.ChunkItem
		one(t, msg, protocol.ItemChunk, &c)
		if err := c.Validate(); err != nil || c.Off != next || c.Total != nil || !bytes.Equal(c.Ctx, ctx) {
			t.Fatalf("chunk at %d: off %d, %v", next, c.Off, err)
		}
		last := c.Off+uint64(len(c.Data)) == uint64(len(body))
		if last != (c.SHA256 != nil) {
			t.Fatalf("chunk at %d: sha256 %x", c.Off, c.SHA256)
		}
		got = append(got, c.Data...)
		if calls > 10 {
			t.Fatal("no end")
		}
	}
	if !bytes.Equal(got, body) {
		t.Fatal("the bytes sent are not the file's")
	}
	r := result(t, msg)
	named, err := protocol.NamedResultData(protocol.ResultFileDownload, r.Data)
	if err != nil {
		t.Fatal(err)
	}
	f, err := protocol.ReadFileDownload(0, named)
	sum := sha256.Sum256(body)
	if err != nil || !r.Done || !*r.OK || f.Path != path || f.Size != uint64(len(body)) || !bytes.Equal(f.SHA256, sum[:]) || !bytes.Equal(f.Xfer, xfer) {
		t.Fatalf("result %+v: %+v %v", r, f, err)
	}

	// A file that is not there fails the task at once.
	_, msg = call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: taskID(), Cmd: opDownload, Args: protocol.Args{1: filepath.Join(dir, "none")}})
	if r := result(t, msg); *r.OK || r.Err == "" {
		t.Fatalf("a missing file: %+v", r)
	}
}

func TestPageUpload(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	dir := t.TempDir()
	body := make([]byte, protocol.MaxChunkData+3000)
	_, _ = rand.Read(body)
	sum := sha256.Sum256(body)
	task, xfer := taskID(), taskID()
	offer := protocol.FileOffer{Xfer: xfer, Name: "up.bin", Size: uint64(len(body)), SHA256: sum[:]}

	// The page gets ready and asks for the first chunk, with its ctx.
	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: task, Cmd: opUpload, Args: protocol.Args{1: offer, 2: dir, 3: false}})
	// Two acks: the task's, and the transfer's ask.
	ask := func(msg *protocol.MinionMessage) protocol.AckItem {
		t.Helper()
		for _, raw := range byType(t, msg)[protocol.ItemAck] {
			var a protocol.AckItem
			if protocol.UnmarshalCBOR(raw, &a) == nil && a.Xfer != nil {
				return a
			}
		}
		t.Fatalf("no ask in %v", byType(t, msg))
		return protocol.AckItem{}
	}
	a := ask(msg)
	if !bytes.Equal(a.Xfer, xfer) || a.Next == nil || *a.Next != 0 || len(a.Ctx) == 0 {
		t.Fatalf("first ask %+v", a)
	}
	ctx := a.Ctx
	chunk := func(off uint64) protocol.ChunkItem {
		end := min(off+protocol.MaxChunkData, uint64(len(body)))
		c := protocol.ChunkItem{T: protocol.ItemChunk, Xfer: xfer, Off: off, Data: body[off:end], Ctx: ctx}
		if off == 0 {
			total := uint64(len(body))
			c.Total, c.Task = &total, task
		}
		if end == uint64(len(body)) {
			c.SHA256 = sum[:]
		}
		return c
	}
	_, msg = call(t, url, "POST", session, testKeys, chunk(0))
	if a = ask(msg); *a.Next != protocol.MaxChunkData || !bytes.Equal(a.Ctx, ctx) {
		t.Fatalf("after the first chunk %+v", a)
	}
	// The same chunk again is written once.
	_, msg = call(t, url, "POST", session, testKeys, chunk(0))
	if a = ask(msg); *a.Next != protocol.MaxChunkData {
		t.Fatalf("after a chunk sent twice %+v", a)
	}
	_, msg = call(t, url, "POST", session, testKeys, chunk(protocol.MaxChunkData))
	if a = ask(msg); *a.Next != uint64(len(body)) {
		t.Fatalf("the last ask %+v", a)
	}
	r := result(t, msg)
	named, _ := protocol.NamedResultData(protocol.ResultFileUpload, r.Data)
	f, err := protocol.ReadFileUpload(0, named)
	dest := filepath.Join(dir, "up.bin")
	if err != nil || !*r.OK || f.Path != dest || f.Size != uint64(len(body)) || !bytes.Equal(f.SHA256, sum[:]) {
		t.Fatalf("result %+v: %+v %v", r, f, err)
	}
	if written, _ := os.ReadFile(dest); !bytes.Equal(written, body) {
		t.Fatal("the file written is not the one sent")
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, ".*.part")); len(parts) != 0 {
		t.Fatalf("temporary files left: %v", parts)
	}

	// Already there, without overwrite: refused before anything is asked.
	_, msg = call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: taskID(), Cmd: opUpload, Args: protocol.Args{1: offer, 2: dest, 3: false}})
	if r := result(t, msg); *r.OK {
		t.Fatalf("over a file: %+v", r)
	}
	// A file that does not check out: failed, its temporary file removed.
	other := protocol.FileOffer{Xfer: taskID(), Name: "bad.bin", Size: 3, SHA256: make([]byte, 32)}
	bad := taskID()
	_, msg = call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: bad, Cmd: opUpload, Args: protocol.Args{1: other, 2: dir}})
	a = ask(msg)
	total := uint64(3)
	_, msg = call(t, url, "POST", session, testKeys, protocol.ChunkItem{T: protocol.ItemChunk, Xfer: other.Xfer, Data: []byte("abc"),
		Total: &total, Task: bad, SHA256: make([]byte, 32), Ctx: a.Ctx})
	if r := result(t, msg); *r.OK || r.Err == "" {
		t.Fatalf("a bad file: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.bin")); err == nil {
		t.Fatal("a bad file put in place")
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, ".*.part")); len(parts) != 0 {
		t.Fatalf("temporary files left: %v", parts)
	}
	// A chunk the page did not ask for (no ctx): an err.
	_, msg = call(t, url, "POST", session, testKeys, protocol.ChunkItem{T: protocol.ItemChunk, Xfer: xfer, Off: 5, Data: []byte("x")})
	if len(byType(t, msg)[protocol.ItemErr]) != 1 {
		t.Fatalf("a chunk not asked for: %v", byType(t, msg))
	}
}
