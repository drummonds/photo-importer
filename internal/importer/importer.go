// Package importer drives the photo sync: resolve source, run rsync, run hooks.
package importer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codeberg.org/hum3/photo-importer/internal/config"
	"codeberg.org/hum3/photo-importer/internal/mtp"
)

type Options struct {
	DryRun  bool
	Verbose bool
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

// Sync runs rsync from resolved.Source to resolved.Destination using
// MTP-friendly defaults plus any extra args from the config.
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

	args := defaultRsyncArgs(opts)
	args = append(args, cfg.RsyncArgs...)
	// Trailing slash on source: copy contents, not the directory itself.
	args = append(args, ensureSlash(r.Source), ensureSlash(r.Destination))

	fmt.Printf("rsync %s\n", quoted(args))
	cmd := exec.Command("rsync", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rsync: %w", err)
	}

	if opts.DryRun {
		fmt.Println("(dry-run: post-sync hooks skipped)")
		return nil
	}
	return runHooks(cfg.PostSync, r)
}

// defaultRsyncArgs are tuned for MTP/gvfs mounts which have unreliable
// timestamps and no useful permissions.
func defaultRsyncArgs(opts Options) []string {
	a := []string{
		"-rt",                // recurse, preserve mtimes (best effort)
		"--no-perms",         // MTP perms are meaningless
		"--omit-dir-times",   // dir mtimes can't be set on MTP
		"--modify-window=2",  // FAT-style 2s tolerance
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

func runHooks(hooks []string, r Resolved) error {
	if len(hooks) == 0 {
		return nil
	}
	env := append(os.Environ(),
		"PHOTO_IMPORTER_SOURCE="+r.Source,
		"PHOTO_IMPORTER_DEST="+r.Destination,
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
