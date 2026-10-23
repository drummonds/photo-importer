// Package manifest persists the set of relative paths that have already
// been transferred, so subsequent syncs can exclude them via rsync
// --exclude-from. This is more reliable than relying on MTP mtimes.
package manifest

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Manifest struct {
	path  string
	paths map[string]struct{}
}

// Path returns the manifest file path under XDG_STATE_HOME (or the
// $HOME/.local/state fallback).
func Path() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "photo-importer", "transferred.txt"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "photo-importer", "transferred.txt"), nil
}

// Load reads the manifest. A missing file yields an empty manifest.
func Load() (*Manifest, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	m := &Manifest{path: p, paths: map[string]struct{}{}}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return m, nil
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
		m.paths[line] = struct{}{}
	}
	return m, sc.Err()
}

func (m *Manifest) Has(p string) bool { _, ok := m.paths[p]; return ok }
func (m *Manifest) Len() int          { return len(m.paths) }
func (m *Manifest) FilePath() string  { return m.path }

// Add appends paths to the manifest file (deduplicating against what's
// already loaded).
func (m *Manifest) Add(paths []string) (added int, err error) {
	if len(paths) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(m.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	sort.Strings(paths)
	for _, p := range paths {
		if _, ok := m.paths[p]; ok {
			continue
		}
		if _, err := fmt.Fprintln(f, p); err != nil {
			_ = f.Close()
			return added, err
		}
		m.paths[p] = struct{}{}
		added++
	}
	return added, f.Close()
}

// WriteExcludeFile writes the manifest paths in rsync exclude-from format
// (each anchored to the source root with a leading "/") to a temp file.
// Returns "" if the manifest is empty. Caller is responsible for removing
// the file.
func (m *Manifest) WriteExcludeFile() (string, error) {
	if len(m.paths) == 0 {
		return "", nil
	}
	f, err := os.CreateTemp("", "photo-importer-exclude-*.txt")
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(m.paths))
	for p := range m.paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	w := bufio.NewWriter(f)
	for _, p := range paths {
		if _, err := fmt.Fprintln(w, "/"+p); err != nil {
			_ = f.Close()
			return "", err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}
