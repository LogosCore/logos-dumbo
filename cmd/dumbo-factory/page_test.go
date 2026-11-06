package main

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/logoscore/logos-golang-protocol/protocol"
)

// The page under real PHP (php on PATH; skipped without it), spoken to as
// core speaks to it through the Dumbo channel: its first call a rehello
// under the build id, the calls after under a session id, each body the id
// and the sealed message.

// startPage serves a page of req with PHP's built-in server and returns its
// URL.
func startPage(t *testing.T, req protocol.BuildRequest) string {
	t.Helper()
	php, err := exec.LookPath("php")
	if err != nil {
		// CI sets DUMBO_REQUIRE_PHP: there a missing php fails, not skips.
		if os.Getenv("DUMBO_REQUIRE_PHP") != "" {
			t.Fatal("no php on PATH")
		}
		t.Skip("no php on PATH")
	}
	page, _, err := render(req, "test")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.php"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // only to find a free port
	cmd := exec.Command(php, "-S", addr, "-t", dir)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	for deadline := time.Now().Add(30 * time.Second); ; {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("php did not listen on %s", addr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "http://" + addr + "/page.php"
}

// call sends items under id sealed with keys, with method, and returns the
// page's status and, for a 200, its answer, checked to answer this message.
// It says core's time (at), as core does.
func call(t *testing.T, url, method string, id [16]byte, keys protocol.AES256CBCHMACSHA256Material, items ...any) (int, *protocol.MinionMessage) {
	t.Helper()
	return callAt(t, url, method, id, keys, uint64(time.Now().Unix()), items...)
}

// callAt is call saying at as core's time (0: none).
func callAt(t *testing.T, url, method string, id [16]byte, keys protocol.AES256CBCHMACSHA256Material, at uint64, items ...any) (int, *protocol.MinionMessage) {
	t.Helper()
	raws := make([]cbor.RawMessage, len(items))
	for i, it := range items {
		b, err := protocol.MarshalCBOR(it)
		if err != nil {
			t.Fatal(err)
		}
		raws[i] = b
	}
	msgID := protocol.NewMessageID()
	plain, err := protocol.MarshalCBOR(protocol.CoreMessage{V: protocol.MinionMessageV3, ID: msgID, Items: raws, At: at})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := protocol.SealFrame(keys, id, protocol.FrameCoreToMinion, plain)
	if err != nil {
		t.Fatal(err)
	}
	return post(t, url, method, append(id[:], sealed...), id, keys, msgID)
}

func post(t *testing.T, url, method string, body []byte, id [16]byte, keys protocol.AES256CBCHMACSHA256Material, msgID []byte) (int, *protocol.MinionMessage) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content type %q: %s", ct, got)
	}
	if len(got) < 16 || !bytes.Equal(got[:16], id[:]) {
		t.Fatalf("an answer under another id: % x", got[:min(len(got), 16)])
	}
	opened, err := protocol.OpenFrame(keys, id, protocol.FrameMinionToCore, got[16:])
	if err != nil {
		t.Fatalf("the answer does not open: %v", err)
	}
	var msg protocol.MinionMessage
	if err := protocol.UnmarshalCBOR(opened, &msg); err != nil {
		t.Fatalf("the answer is not CBOR: %v", err)
	}
	if err := protocol.ValidateMessageHead(msg.V, msg.ID, msg.Re, len(msg.Items)); err != nil || !bytes.Equal(msg.Re, msgID) {
		t.Fatalf("the answer's head: %v (re % x)", err, msg.Re)
	}
	return resp.StatusCode, &msg
}

// byType are a message's items by their type.
func byType(t *testing.T, msg *protocol.MinionMessage) map[int][]cbor.RawMessage {
	t.Helper()
	out := map[int][]cbor.RawMessage{}
	for _, raw := range msg.Items {
		out[protocol.ItemType(raw)] = append(out[protocol.ItemType(raw)], raw)
	}
	return out
}

func taskID() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}

func TestPage(t *testing.T) {
	req := request(t, "PUT")
	url := startPage(t, req)
	var buildID [16]byte
	copy(buildID[:], mustUUID(t, req.BuildID))

	// The first call: a rehello under the build id. The page says hello,
	// through its connection, and takes tasks.
	status, msg := call(t, url, "PUT", buildID, testKeys, protocol.RehelloItem{T: protocol.ItemRehello})
	if status != http.StatusOK {
		t.Fatalf("rehello: HTTP %d", status)
	}
	items := byType(t, msg)
	if len(items[protocol.ItemHello]) != 1 || len(items[protocol.ItemGetTasks]) != 1 {
		t.Fatalf("rehello answered with %d items: %v", len(msg.Items), items)
	}
	var hello protocol.HelloItem
	if err := protocol.UnmarshalCBOR(items[protocol.ItemHello][0], &hello); err != nil {
		t.Fatal(err)
	}
	if err := hello.Validate(); err != nil {
		t.Fatalf("hello %+v: %v", hello, err)
	}
	if hello.Conn != 1 || hello.Version != "dumbo-page/test" || hello.OS == "" || hello.Arch == "" || hello.MachineSource == 0 ||
		hello.X["php"] == nil || hello.X["sapi"] != "cli-server" {
		t.Fatalf("hello %+v", hello)
	}
	var get protocol.GetTasksItem
	if err := protocol.UnmarshalCBOR(items[protocol.ItemGetTasks][0], &get); err != nil || get.Max == nil || *get.Max != 4 {
		t.Fatalf("get_tasks %+v %v", get, err)
	}

	// Under a session id: tasks run within the call, acknowledged and
	// answered at once; a cancel finds no task; an item a page does not
	// take is said so.
	var session [16]byte
	copy(session[:], taskID())
	hi, other := taskID(), taskID()
	status, msg = call(t, url, "PUT", session, testKeys,
		protocol.TaskItem{T: protocol.ItemTask, Task: hi, Cmd: opHello, Args: protocol.Args{1: "x", 2: map[string]any{"k": 1}}},
		protocol.TaskItem{T: protocol.ItemTask, Task: other, Cmd: 9},
		protocol.CancelItem{T: protocol.ItemCancel, Task: other},
		protocol.StageItem{T: protocol.ItemStage, Key: []byte{1}, Corr: taskID()},
	)
	if status != http.StatusOK {
		t.Fatalf("tasks: HTTP %d", status)
	}
	items = byType(t, msg)
	var ack protocol.AckItem
	if len(items[protocol.ItemAck]) != 1 || protocol.UnmarshalCBOR(items[protocol.ItemAck][0], &ack) != nil ||
		len(ack.Tasks) != 2 || !bytes.Equal(ack.Tasks[0], hi) || !bytes.Equal(ack.Tasks[1], other) {
		t.Fatalf("ack %+v", ack)
	}
	results := map[string]protocol.ResultItem{}
	for _, raw := range items[protocol.ItemResult] {
		var r protocol.ResultItem
		if err := protocol.UnmarshalCBOR(raw, &r); err != nil || r.Validate() != nil {
			t.Fatalf("result %+v: %v", r, err)
		}
		results[string(r.Task)] = r
	}
	if r := results[string(hi)]; !r.Done || r.OK == nil || !*r.OK || string(r.Out) != "it's a \\ page\n" {
		t.Fatalf("hello's result %+v", r)
	}
	if r := results[string(other)]; !r.Done || r.OK == nil || *r.OK || !strings.Contains(r.Err, "command 9") {
		t.Fatalf("an unknown command's result %+v", r)
	}
	errs := map[int]int{}
	for _, raw := range items[protocol.ItemErr] {
		var e protocol.ErrItem
		if err := protocol.UnmarshalCBOR(raw, &e); err != nil || e.Item == nil {
			t.Fatalf("err %+v: %v", e, err)
		}
		errs[*e.Item] = e.Code
	}
	if errs[2] != protocol.ErrItemUnknownTask || errs[3] != protocol.ErrItemUnknownItem {
		t.Fatalf("errs by item %v", errs)
	}

	// Core's acks and a hello_response need no answer but the page's
	// get_tasks.
	status, msg = call(t, url, "PUT", session, testKeys, protocol.AckItem{T: protocol.ItemAck, Tasks: [][]byte{hi}})
	if status != http.StatusOK || len(msg.Items) != 1 || protocol.ItemType(msg.Items[0]) != protocol.ItemGetTasks {
		t.Fatalf("an ack answered with %d %v", status, msg)
	}
}

// Anything but a message of its build, with its method, gets the 404 a
// missing page would.
func TestPageNotFound(t *testing.T) {
	req := request(t, "POST")
	url := startPage(t, req)
	var buildID [16]byte
	copy(buildID[:], mustUUID(t, req.BuildID))
	other := protocol.AES256CBCHMACSHA256Material{EncKey: bytes.Repeat([]byte{3}, 32), MACKey: bytes.Repeat([]byte{4}, 32)}
	rehello := protocol.RehelloItem{T: protocol.ItemRehello}
	if status, _ := call(t, url, "PUT", buildID, testKeys, rehello); status != http.StatusNotFound {
		t.Errorf("another method: HTTP %d", status)
	}
	if status, _ := call(t, url, "POST", buildID, other, rehello); status != http.StatusNotFound {
		t.Errorf("another build's keys: HTTP %d", status)
	}
	if status, _ := post(t, url, "POST", bytes.Repeat([]byte{7}, 200), buildID, testKeys, nil); status != http.StatusNotFound {
		t.Errorf("garbage: HTTP %d", status)
	}
	if status, _ := post(t, url, "POST", nil, buildID, testKeys, nil); status != http.StatusNotFound {
		t.Errorf("an empty body: HTTP %d", status)
	}
	// A message that opens but is not CBOR.
	sealed, err := protocol.SealFrame(testKeys, buildID, protocol.FrameCoreToMinion, []byte{0xff, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := post(t, url, "POST", append(buildID[:], sealed...), buildID, testKeys, nil); status != http.StatusNotFound {
		t.Errorf("not CBOR: HTTP %d", status)
	}
	// The right one answers.
	if status, _ := call(t, url, "POST", buildID, testKeys, rehello); status != http.StatusOK {
		t.Errorf("the page's own: HTTP %d", status)
	}
}

func mustUUID(t *testing.T, s string) []byte {
	t.Helper()
	clean := strings.ReplaceAll(s, "-", "")
	b := make([]byte, 16)
	for i := range b {
		var v byte
		for _, c := range clean[2*i : 2*i+2] {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= byte(c - '0')
			case c >= 'a' && c <= 'f':
				v |= byte(c - 'a' + 10)
			default:
				t.Fatalf("uuid %q", s)
			}
		}
		b[i] = v
	}
	return b
}
