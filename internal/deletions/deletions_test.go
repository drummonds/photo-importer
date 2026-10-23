package deletions

import (
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T) *Queue {
	t.Helper()
	q, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestPathUnderXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/x")
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp/x/photo-importer/deletion-queue.txt"; p != want {
		t.Fatalf("got %q want %q", p, want)
	}
}

func TestMissingFileIsEmptyQueue(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	q := load(t)
	if q.Len() != 0 || len(q.Paths()) != 0 {
		t.Fatalf("expected empty queue, got %v", q.Paths())
	}
}

func TestAddPersistsSortedAndDeduplicated(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	q := load(t)
	if err := q.Add("Camera/b.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("Camera/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("Camera/b.jpg"); err != nil {
		t.Fatal(err)
	}
	if !q.Has("Camera/a.jpg") || q.Len() != 2 {
		t.Fatalf("in-memory state wrong: %v", q.Paths())
	}
	again := load(t)
	got := again.Paths()
	if len(got) != 2 || got[0] != "Camera/a.jpg" || got[1] != "Camera/b.jpg" {
		t.Fatalf("reloaded %v", got)
	}
}

func TestRemovePersists(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	q := load(t)
	for _, p := range []string{"a", "b", "c"} {
		if err := q.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Remove([]string{"b", "zzz-not-there"}); err != nil {
		t.Fatal(err)
	}
	got := load(t).Paths()
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("reloaded %v", got)
	}
}

func TestRemovingLastEntryLeavesEmptyFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	q := load(t)
	if err := q.Add("a"); err != nil {
		t.Fatal(err)
	}
	if err := q.Remove([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if load(t).Len() != 0 {
		t.Fatal("expected empty after remove")
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "photo-importer", "deletion-queue.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("expected empty file, got %q", data)
	}
}

func TestLoadIgnoresBlankAndCommentLines(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	p := filepath.Join(dir, "photo-importer", "deletion-queue.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("# queued\n\nCamera/x.jpg\n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := load(t).Paths()
	if len(got) != 1 || got[0] != "Camera/x.jpg" {
		t.Fatalf("got %v", got)
	}
}
