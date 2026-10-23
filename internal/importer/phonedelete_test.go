package importer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.bytestone.uk/hum3/photo-importer/internal/deletions"
)

func setupPhone(t *testing.T, queued ...string) (src string, q *deletions.Queue) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	src = t.TempDir()
	q, err := deletions.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range queued {
		if err := q.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	return src, q
}

func touch(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, rel))
	return err == nil
}

func reload(t *testing.T) []string {
	t.Helper()
	q, err := deletions.Load()
	if err != nil {
		t.Fatal(err)
	}
	return q.Paths()
}

func TestEmptyQueueIsSilent(t *testing.T) {
	src, q := setupPhone(t)
	var out bytes.Buffer
	res := applyPhoneDeletions(src, q, false, func(int, []string) bool { t.Fatal("must not prompt"); return false }, &out)
	if res != (phoneDeletionResult{}) || out.Len() != 0 {
		t.Fatalf("res=%+v out=%q", res, out.String())
	}
}

func TestDryRunReportsAndChangesNothing(t *testing.T) {
	src, q := setupPhone(t, "Camera/a.jpg", "Camera/gone.jpg")
	touch(t, src, "Camera/a.jpg")
	var out bytes.Buffer
	res := applyPhoneDeletions(src, q, true, func(int, []string) bool { t.Fatal("must not prompt"); return false }, &out)
	if res.Present != 1 || res.Absent != 1 || res.Deleted != 0 {
		t.Fatalf("res=%+v", res)
	}
	if !exists(src, "Camera/a.jpg") || len(reload(t)) != 2 {
		t.Fatal("dry-run must not delete or edit the queue")
	}
	if !strings.Contains(out.String(), "2 file(s) queued for deletion from the phone") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestDeclinedConfirmationKeepsQueueAndFiles(t *testing.T) {
	src, q := setupPhone(t, "Camera/a.jpg")
	touch(t, src, "Camera/a.jpg")
	var out bytes.Buffer
	asked := 0
	res := applyPhoneDeletions(src, q, false, func(n int, preview []string) bool {
		asked++
		if n != 1 || len(preview) != 1 || preview[0] != "Camera/a.jpg" {
			t.Fatalf("prompt n=%d preview=%v", n, preview)
		}
		return false
	}, &out)
	if asked != 1 || res.Deleted != 0 || res.Skipped != 1 {
		t.Fatalf("asked=%d res=%+v", asked, res)
	}
	if !exists(src, "Camera/a.jpg") || len(reload(t)) != 1 {
		t.Fatal("declining must not delete or dequeue")
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestConfirmedDeletesFromPhoneAndDequeues(t *testing.T) {
	src, q := setupPhone(t, "Camera/a.jpg", "Camera/b.jpg", "Camera/gone.jpg")
	touch(t, src, "Camera/a.jpg")
	touch(t, src, "Camera/b.jpg")
	touch(t, src, "Camera/keep.jpg")
	var out bytes.Buffer
	res := applyPhoneDeletions(src, q, false, func(int, []string) bool { return true }, &out)
	if res.Deleted != 2 || res.Absent != 1 || res.Failed != 0 {
		t.Fatalf("res=%+v", res)
	}
	if exists(src, "Camera/a.jpg") || exists(src, "Camera/b.jpg") || !exists(src, "Camera/keep.jpg") {
		t.Fatal("wrong files deleted")
	}
	if got := reload(t); len(got) != 0 {
		t.Fatalf("queue should be empty, got %v", got)
	}
	if !strings.Contains(out.String(), "deleted 2 file(s) from the phone") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestAlreadyAbsentIsDequeuedWithoutPrompt(t *testing.T) {
	src, q := setupPhone(t, "Camera/gone.jpg")
	var out bytes.Buffer
	res := applyPhoneDeletions(src, q, false, func(int, []string) bool { t.Fatal("nothing to confirm"); return false }, &out)
	if res.Absent != 1 || len(reload(t)) != 0 {
		t.Fatalf("res=%+v queue=%v", res, reload(t))
	}
}

func TestFailedDeleteStaysQueued(t *testing.T) {
	src, q := setupPhone(t, "Camera/dir", "Camera/a.jpg")
	touch(t, src, "Camera/a.jpg")
	touch(t, src, "Camera/dir/inner.jpg") // a non-empty directory cannot be removed
	var out bytes.Buffer
	res := applyPhoneDeletions(src, q, false, func(int, []string) bool { return true }, &out)
	if res.Deleted != 1 || res.Failed != 1 {
		t.Fatalf("res=%+v", res)
	}
	if got := reload(t); len(got) != 1 || got[0] != "Camera/dir" {
		t.Fatalf("queue=%v", got)
	}
}
