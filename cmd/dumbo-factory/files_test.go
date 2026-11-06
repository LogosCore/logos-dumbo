package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logoscore/logos-golang-protocol/protocol"
)

// ls and rm under real PHP.

func TestPageLs(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	dir := t.TempDir()
	randomFile(t, dir, "a.txt", 10)
	randomFile(t, dir, ".hidden", 3)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: taskID(), Cmd: opLs, Args: protocol.Args{1: dir}})
	r := result(t, msg)
	if !*r.OK || len(r.Ls) != 1 {
		t.Fatalf("result %+v", r)
	}
	p := r.Ls[0]
	if p.Path != dir || p.Sep != "/" || p.Parent != filepath.Dir(dir) || !p.Last || p.Partial {
		t.Fatalf("piece %+v", p)
	}
	got := map[string]protocol.LsEntry{}
	for _, e := range p.Entries {
		got[e.Name] = e
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if e := got["a.txt"]; e.Type != protocol.EntryFile || e.Size == nil || *e.Size != 10 || e.Modified == nil || e.Mode == 0 || e.Owner == 0 {
		t.Errorf("a.txt %+v (owners %v)", e, p.Owners)
	}
	if e := got["sub"]; e.Type != protocol.EntryDir || e.Size != nil {
		t.Errorf("sub %+v", e)
	}
	if e := got["link"]; e.Type != protocol.EntryLink || e.Target != "a.txt" {
		t.Errorf("link %+v", e)
	}
	if e := got[".hidden"]; !e.Hidden {
		t.Errorf(".hidden %+v", e)
	}
	if len(got) != 4 {
		t.Errorf("%d entries: %v", len(got), got)
	}

	// Not there, or not a directory: failed.
	for _, bad := range []string{filepath.Join(dir, "none"), filepath.Join(dir, "a.txt")} {
		_, msg = call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: taskID(), Cmd: opLs, Args: protocol.Args{1: bad}})
		if r := result(t, msg); *r.OK || r.Err == "" {
			t.Errorf("%s: %+v", bad, r)
		}
	}
}

func TestPageRm(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	dir := t.TempDir()
	rm := func(path string, recursive bool) protocol.ResultItem {
		t.Helper()
		_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: taskID(), Cmd: opRm, Args: protocol.Args{1: path, 2: recursive}})
		return result(t, msg)
	}
	exists := func(path string) bool { _, err := os.Lstat(path); return err == nil }

	randomFile(t, dir, "a.txt", 4)
	if r := rm(filepath.Join(dir, "a.txt"), false); !*r.OK || !strings.Contains(string(r.Out), "(file)") || exists(filepath.Join(dir, "a.txt")) {
		t.Fatalf("a file: %+v", r)
	}
	// A link goes, what it points to stays.
	randomFile(t, dir, "target.txt", 4)
	_ = os.Symlink("target.txt", filepath.Join(dir, "link"))
	if r := rm(filepath.Join(dir, "link"), false); !*r.OK || !strings.Contains(string(r.Out), "(link)") || exists(filepath.Join(dir, "link")) || !exists(filepath.Join(dir, "target.txt")) {
		t.Fatalf("a link: %+v", r)
	}
	// A folder with something in it only with recursive.
	sub := filepath.Join(dir, "sub")
	_ = os.MkdirAll(filepath.Join(sub, "deeper"), 0o755)
	randomFile(t, filepath.Join(sub, "deeper"), "x", 2)
	if r := rm(sub, false); *r.OK || !strings.Contains(r.Err, "not empty") || !exists(sub) {
		t.Fatalf("a full folder: %+v", r)
	}
	if r := rm(sub, true); !*r.OK || exists(sub) {
		t.Fatalf("recursive: %+v", r)
	}
	// Never a root; nothing that is not there.
	if r := rm("/", true); *r.OK || !strings.Contains(r.Err, "root") {
		t.Fatalf("the root: %+v", r)
	}
	if r := rm(filepath.Join(dir, "none"), false); *r.OK {
		t.Fatalf("not there: %+v", r)
	}
	// Relative to the page's folder, .. resolved: a file beside the page.
	if r := rm("../../../../../"+strings.TrimPrefix(filepath.Join(dir, "target.txt"), "/"), false); !*r.OK || exists(filepath.Join(dir, "target.txt")) {
		t.Fatalf("a relative path: %+v", r)
	}
}
