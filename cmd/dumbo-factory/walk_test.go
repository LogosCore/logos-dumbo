package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/logoscore/logos-golang-protocol/protocol"
)

// A walk under real PHP, the test playing core: each part that is not done
// says where the page stands (ctx), and the test resumes it from there, as
// core does, until a part says done.

// tree makes a folder of wide folders (too much for one answer), a nested
// branch deeper than the walk goes, a hidden folder and a link to a folder.
func tree(t *testing.T) (root string, folders int) {
	t.Helper()
	root = t.TempDir()
	mk := func(rel string) {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
		folders++
	}
	// 5000 entries a folder, as many as a listing says: b has 4999 files
	// and its folder x.
	for _, d := range []string{"a", "b", "c", "d"} {
		mk(d)
		n := 5000
		if d == "b" {
			n = 4999
		}
		for i := 0; i < n; i++ {
			if err := os.WriteFile(filepath.Join(root, d, fmt.Sprintf("file-%05d.txt", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("b/x")
	mk("b/x/y")
	mk("b/x/y/z") // depth 4: past depth 3
	mk(".hidden")
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root, folders
}

func walkData(t *testing.T, r protocol.ResultItem) protocol.DirWalk {
	t.Helper()
	named, err := protocol.NamedResultData(protocol.ResultDirWalk, r.Data)
	if err != nil {
		t.Fatal(err)
	}
	w, err := protocol.ReadDirWalk(r.Seq, named)
	if err != nil {
		t.Fatal(err)
	}
	return *w
}

func TestPageWalkAcrossCalls(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	root, _ := tree(t)
	task := taskID()

	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: task, Cmd: opWalk,
		Args: protocol.Args{1: root, 2: 3, 3: 0, 4: false, 5: true}})
	var listed []string
	var r protocol.ResultItem
	for calls := 1; ; calls++ {
		r = result(t, msg)
		if r.Seq != uint64(calls-1) {
			t.Fatalf("part %d has seq %d", calls-1, r.Seq)
		}
		for _, p := range r.Ls {
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			if p.Part == 0 {
				listed = append(listed, p.Path)
			}
		}
		if r.Done {
			break
		}
		if len(r.Ctx) == 0 || calls > 20 {
			t.Fatalf("part %d: no ctx, or no end", calls)
		}
		w := walkData(t, r)
		if w.Current == "" {
			t.Fatal("a part under way says no current folder")
		}
		// As core: the part answered with a resume giving its ctx back.
		_, msg = call(t, url, "POST", session, testKeys, protocol.ResumeItem{T: protocol.ItemResume, Task: task, Ctx: r.Ctx})
	}
	if !*r.OK || r.Seq == 0 {
		t.Fatalf("the walk ended ok %v after %d parts", *r.OK, r.Seq+1)
	}
	// Depth first, names in order; not the hidden folder, the link, or
	// b/x/y/z (depth 4).
	want := []string{root, filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "b", "x"),
		filepath.Join(root, "b", "x", "y"), filepath.Join(root, "c"), filepath.Join(root, "d")}
	if fmt.Sprint(listed) != fmt.Sprint(want) {
		t.Fatalf("listed %v\nwant %v", listed, want)
	}
	w := walkData(t, r)
	// Entries: the root's 6 (a b c d .hidden link), 5000 in each of a, b,
	// c, d, 1 in b/x and b/x/y.
	if w.Root != root || w.Folders != 7 || w.Entries != 6+4*5000+1+1 || w.Skipped != 2 || w.Current != "" {
		t.Fatalf("walk %+v", w)
	}
}

func TestPageWalkStopped(t *testing.T) {
	url := startPage(t, request(t, "POST"))
	var session [16]byte
	copy(session[:], taskID())
	root, _ := tree(t)
	task := taskID()
	_, msg := call(t, url, "POST", session, testKeys, protocol.TaskItem{T: protocol.ItemTask, Task: task, Cmd: opWalk, Args: protocol.Args{1: root}})
	r := result(t, msg)
	if r.Done {
		t.Fatalf("the walk ended in one call (%d listings): the tree is too small to test a stop", len(r.Ls))
	}
	// As core: the operator stopped it; the cancel says where it stood.
	_, msg = call(t, url, "POST", session, testKeys, protocol.CancelItem{T: protocol.ItemCancel, Task: task, Ctx: r.Ctx})
	end := result(t, msg)
	if !end.Done || *end.OK || end.Err != "stopped" || end.Seq != r.Seq+1 {
		t.Fatalf("stopped: %+v", end)
	}
	// A cancel without a ctx: a task the page no longer has, named so core
	// ends it.
	gone := taskID()
	_, msg = call(t, url, "POST", session, testKeys, protocol.CancelItem{T: protocol.ItemCancel, Task: gone})
	var e protocol.ErrItem
	if errs := byType(t, msg)[protocol.ItemErr]; len(errs) != 1 || protocol.UnmarshalCBOR(errs[0], &e) != nil ||
		e.Code != protocol.ErrItemUnknownTask || !bytes.Equal(e.Task, gone) {
		t.Fatalf("a cancel without ctx: %+v", e)
	}
}
