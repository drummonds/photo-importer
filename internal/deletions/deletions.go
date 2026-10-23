// Package deletions persists the deletion queue: destination-relative
// paths of photos the user has deleted locally in the editor and that
// still need deleting from the phone on the next sync.
package deletions

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Queue is the set of relative paths awaiting deletion from the phone,
// backed by one file (one path per line).
type Queue struct {
	path  string
	paths map[string]struct{}
}

// Path returns the queue file under XDG_STATE_HOME (or ~/.local/state).
func Path() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "photo-importer", "deletion-queue.txt"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "photo-importer", "deletion-queue.txt"), nil
}

// Load reads the queue. A missing file yields an empty queue.
func Load() (*Queue, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	q := &Queue{path: p, paths: map[string]struct{}{}}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return q, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only file
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		q.paths[line] = struct{}{}
	}
	return q, sc.Err()
}

func (q *Queue) Has(p string) bool { _, ok := q.paths[p]; return ok }
func (q *Queue) Len() int          { return len(q.paths) }
func (q *Queue) FilePath() string  { return q.path }

// Paths returns the queued paths, sorted.
func (q *Queue) Paths() []string {
	out := make([]string, 0, len(q.paths))
	for p := range q.paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Add queues one path and persists the queue. Adding a path already
// queued is a no-op.
func (q *Queue) Add(p string) error {
	if q.Has(p) {
		return nil
	}
	q.paths[p] = struct{}{}
	return q.save()
}

// Remove drops paths from the queue (unknown paths are ignored) and
// persists the result.
func (q *Queue) Remove(paths []string) error {
	changed := false
	for _, p := range paths {
		if _, ok := q.paths[p]; ok {
			delete(q.paths, p)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return q.save()
}

// save rewrites the whole file atomically (write temp, rename).
func (q *Queue) save() error {
	if err := os.MkdirAll(filepath.Dir(q.path), 0o755); err != nil {
		return err
	}
	tmp := q.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, p := range q.Paths() {
		if _, err := w.WriteString(p + "\n"); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, q.path)
}
