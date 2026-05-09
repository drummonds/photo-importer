// Package importer drives the photo sync: resolve source, walk it,
// rsync new files, run hooks.
package importer

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codeberg.org/hum3/photo-importer/internal/config"
	"codeberg.org/hum3/photo-importer/internal/manifest"
	"codeberg.org/hum3/photo-importer/internal/mtp"
)

type Options struct {
	DryRun     bool
	Verbose    bool
	NoManifest bool // disable manifest tracking & exclusion
}

// Resolved holds the concrete paths used for a sync after MTP discovery.
type Resolved struct {
	Source      string
	Destination string
}

// Resolve picks the source path (auto-detecting MTP if cfg.Source is empty)
// and joins the configured sub-path. The destination is taken verbatim.
func Resolve(cfg config.Config) (Resolved, error) {
	src := cfg.Source
	if src == "" {
		m, err := mtp.First()
		if err != nil {
			return Resolved{}, err
		}
		src = m.Path
	}
	if cfg.SubPath != "" {
		src = filepath.Join(src, cfg.SubPath)
	}
	if cfg.Destination == "" {
		return Resolved{}, fmt.Errorf("destination is empty — run `photo-importer config init` and edit the file")
	}
	return Resolved{Source: src, Destination: cfg.Destination}, nil
}

// Sync resolves paths, walks the source, runs rsync on the new files only,
// then appends them to the manifest.
func Sync(cfg config.Config, opts Options) error {
	r, err := Resolve(cfg)
	if err != nil {
		return err
	}
	if _, err := os.Stat(r.Source); err != nil {
		return fmt.Errorf("source not readable (%s): %w", r.Source, err)
	}
	if err := os.MkdirAll(r.Destination, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	var m *manifest.Manifest
	if !opts.NoManifest {
		m, err = manifest.Load()
		if err != nil {
			return fmt.Errorf("load manifest: %w", err)
		}
		if m.Len() > 0 {
			fmt.Printf("manifest: %d previously-transferred file(s) loaded from %s\n", m.Len(), m.FilePath())
		}
	}

	fmt.Printf("scanning source: %s\n", r.Source)
	all, dirs, manifestSkipped, err := enumerate(r, m)
	if err != nil {
		return fmt.Errorf("enumerate source: %w", err)
	}
	pending := make([]string, 0, len(all)-manifestSkipped)
	for _, p := range all {
		if m == nil || !m.Has(p) {
			pending = append(pending, p)
		}
	}
	if m != nil && manifestSkipped > 0 {
		fmt.Printf("scan complete: %d file(s) across %d dir(s); %d already imported, %d new to transfer\n",
			len(all), dirs, manifestSkipped, len(pending))
	} else {
		fmt.Printf("scan complete: %d file(s) across %d dir(s); %d to transfer\n",
			len(all), dirs, len(pending))
	}

	if len(pending) == 0 {
		fmt.Println("nothing to do — all files already at destination")
		return runHooks(cfg.PostSync, r, 0)
	}

	if opts.DryRun {
		previewPending(pending)
		fmt.Println("(dry-run: no files copied, manifest untouched)")
		return nil
	}

	listFile, err := writeFilesList(pending)
	if err != nil {
		return fmt.Errorf("write files list: %w", err)
	}
	defer os.Remove(listFile)

	args := defaultRsyncArgs(opts)
	args = append(args, "--files-from="+listFile)
	args = append(args, cfg.RsyncArgs...)
	args = append(args, ensureSlash(r.Source), ensureSlash(r.Destination))

	fmt.Printf("rsync %s\n", quoted(args))
	cmd := exec.Command("rsync", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rsync: %w", err)
	}

	if m != nil {
		added, err := m.Add(pending)
		if err != nil {
			return fmt.Errorf("update manifest: %w", err)
		}
		fmt.Printf("manifest: +%d file(s) (total %d, %s)\n", added, m.Len(), m.FilePath())
	}

	return runHooks(cfg.PostSync, r, len(pending))
}

// enumerate walks the source directory, returning relative file paths and
// counting how many are already in the manifest. Prints live progress.
func enumerate(r Resolved, m *manifest.Manifest) (files []string, dirCount, manifestSkipped int, err error) {
	var lastReport time.Time
	const tick = 250 * time.Millisecond

	err = filepath.WalkDir(r.Source, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			fmt.Fprintf(os.Stderr, "  warn: %s: %v\n", p, walkErr)
			return nil
		}
		if d.IsDir() {
			dirCount++
			return nil
		}
		rel, e := filepath.Rel(r.Source, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		files = append(files, rel)
		if m != nil && m.Has(rel) {
			manifestSkipped++
		}
		if time.Since(lastReport) > tick {
			fmt.Printf("\r  ... %d files seen (%d in manifest)", len(files), manifestSkipped)
			lastReport = time.Now()
		}
		return nil
	})
	// Final progress line.
	fmt.Printf("\r  ... %d files seen (%d in manifest)\n", len(files), manifestSkipped)
	return
}

func writeFilesList(paths []string) (string, error) {
	f, err := os.CreateTemp("", "photo-importer-files-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, p := range paths {
		if _, err := fmt.Fprintln(w, p); err != nil {
			return "", err
		}
	}
	if err := w.Flush(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func previewPending(files []string) {
	const limit = 20
	n := min(len(files), limit)
	for _, f := range files[:n] {
		fmt.Println("  ", f)
	}
	if len(files) > limit {
		fmt.Printf("   ... and %d more\n", len(files)-limit)
	}
}

// defaultRsyncArgs are tuned for MTP/gvfs mounts which have unreliable
// timestamps and no useful permissions.
func defaultRsyncArgs(opts Options) []string {
	a := []string{
		"-rt",
		"--no-perms",
		"--omit-dir-times",
		"--modify-window=2",
		"--human-readable",
		"--info=progress2",
	}
	if opts.Verbose {
		a = append(a, "-v")
	}
	if opts.DryRun {
		a = append(a, "--dry-run")
	}
	return a
}

func runHooks(hooks []string, r Resolved, transferred int) error {
	if len(hooks) == 0 {
		return nil
	}
	env := append(os.Environ(),
		"PHOTO_IMPORTER_SOURCE="+r.Source,
		"PHOTO_IMPORTER_DEST="+r.Destination,
		fmt.Sprintf("PHOTO_IMPORTER_FILES_TRANSFERRED=%d", transferred),
	)
	for i, h := range hooks {
		fmt.Printf("hook[%d]: %s\n", i, h)
		cmd := exec.Command("sh", "-c", h)
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("hook[%d] failed: %w", i, err)
		}
	}
	return nil
}

func ensureSlash(p string) string {
	if strings.HasSuffix(p, string(os.PathSeparator)) {
		return p
	}
	return p + string(os.PathSeparator)
}

func quoted(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		if strings.ContainsAny(a, " \t\"'") {
			b.WriteString("'" + strings.ReplaceAll(a, "'", `'\''`) + "'")
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}
