// Package dedupe finds byte-identical duplicate files under a directory
// tree and (optionally) deletes all but the one with the most "canonical"
// path. Canonical = shortest path depth, lexicographic tie-break.
//
// The typical use is cleaning up `<dest>/DCIM/DCIM/...` subtrees left
// behind by botched rsync runs.
package dedupe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Set struct {
	Size   int64
	Hash   string
	Keep   string   // path relative to Plan.Root
	Delete []string // paths relative to Plan.Root
}

type Plan struct {
	Root       string
	Sets       []Set
	TotalDups  int   // count of files marked for deletion
	BytesFreed int64 // bytes that would be freed by deleting them
	TotalFiles int
	TotalBytes int64
	Skipped    int // files we couldn't hash (non-regular, unreadable, etc.)
}

// Build walks root, identifies duplicate sets, and returns a deletion
// plan. It does not delete anything.
func Build(root string) (*Plan, error) {
	p := &Plan{Root: root}

	type entry struct {
		rel  string
		size int64
	}
	var files []entry
	var lastReport time.Time
	const tick = 250 * time.Millisecond

	fmt.Printf("scanning %s\n", root)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			fmt.Fprintf(os.Stderr, "  warn: %s: %v\n", path, walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Only consider regular files (skip symlinks, devices, sockets).
		info, e := d.Info()
		if e != nil {
			p.Skipped++
			return nil
		}
		if !info.Mode().IsRegular() {
			p.Skipped++
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		files = append(files, entry{rel: filepath.ToSlash(rel), size: info.Size()})
		p.TotalFiles++
		p.TotalBytes += info.Size()
		if time.Since(lastReport) > tick {
			fmt.Printf("\r  ... %d files (%s)", p.TotalFiles, HumanSize(p.TotalBytes))
			lastReport = time.Now()
		}
		return nil
	})
	fmt.Printf("\r  ... %d files (%s)\n", p.TotalFiles, HumanSize(p.TotalBytes))
	if err != nil {
		return p, err
	}

	// Group by size; only files sharing a size with at least one other
	// can possibly be duplicates.
	bySize := map[int64][]entry{}
	for _, f := range files {
		if f.size == 0 {
			continue // ignore empty files — never useful to dedupe
		}
		bySize[f.size] = append(bySize[f.size], f)
	}
	var candidates []entry
	for _, group := range bySize {
		if len(group) > 1 {
			candidates = append(candidates, group...)
		}
	}
	if len(candidates) == 0 {
		fmt.Println("size-grouping: no files share a size — nothing to hash")
		return p, nil
	}
	fmt.Printf("size-grouping: %d file(s) share a size — hashing\n", len(candidates))

	type hashed struct {
		rel  string
		size int64
		hash string
	}
	hashes := make([]hashed, 0, len(candidates))
	var hashedBytes int64
	lastReport = time.Time{}
	for i, f := range candidates {
		full := filepath.Join(root, filepath.FromSlash(f.rel))
		h, err := hashFile(full)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warn: hash %s: %v\n", f.rel, err)
			p.Skipped++
			continue
		}
		hashes = append(hashes, hashed{f.rel, f.size, h})
		hashedBytes += f.size
		if time.Since(lastReport) > 500*time.Millisecond {
			fmt.Printf("\r  ... hashed %d/%d (%s read)", i+1, len(candidates), HumanSize(hashedBytes))
			lastReport = time.Now()
		}
	}
	fmt.Printf("\r  ... hashed %d/%d (%s read)\n", len(hashes), len(candidates), HumanSize(hashedBytes))

	// Group by (size,hash) — size is implied since equal-hash differing-
	// size shouldn't happen, but include it as a sanity belt.
	byHash := map[string][]hashed{}
	for _, h := range hashes {
		key := fmt.Sprintf("%d:%s", h.size, h.hash)
		byHash[key] = append(byHash[key], h)
	}
	for _, group := range byHash {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			di := strings.Count(group[i].rel, "/")
			dj := strings.Count(group[j].rel, "/")
			if di != dj {
				return di < dj
			}
			return group[i].rel < group[j].rel
		})
		keep := group[0]
		s := Set{Size: keep.size, Hash: keep.hash, Keep: keep.rel}
		for _, d := range group[1:] {
			s.Delete = append(s.Delete, d.rel)
		}
		p.Sets = append(p.Sets, s)
		p.TotalDups += len(group) - 1
		p.BytesFreed += int64(len(group)-1) * keep.size
	}
	sort.Slice(p.Sets, func(i, j int) bool { return p.Sets[i].Keep < p.Sets[j].Keep })
	return p, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only file
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Apply executes the plan: deletes every file in Set.Delete and, if
// removeEmpty is true, walks up and removes any directories that become
// empty (stopping at p.Root).
type ApplyResult struct {
	Deleted     int
	Freed       int64
	Errors      int
	DirsRemoved int
}

func Apply(p *Plan, removeEmpty bool) ApplyResult {
	var res ApplyResult
	emptyCandidates := map[string]struct{}{}
	var lastReport time.Time

	for _, s := range p.Sets {
		for _, d := range s.Delete {
			full := filepath.Join(p.Root, filepath.FromSlash(d))
			if err := os.Remove(full); err != nil {
				fmt.Fprintf(os.Stderr, "  warn: remove %s: %v\n", d, err)
				res.Errors++
				continue
			}
			res.Deleted++
			res.Freed += s.Size
			if removeEmpty {
				emptyCandidates[filepath.Dir(full)] = struct{}{}
			}
			if time.Since(lastReport) > 250*time.Millisecond {
				fmt.Printf("\r  ... deleted %d/%d (%s freed)", res.Deleted, p.TotalDups, HumanSize(res.Freed))
				lastReport = time.Now()
			}
		}
	}
	fmt.Printf("\r  ... deleted %d/%d (%s freed)\n", res.Deleted, p.TotalDups, HumanSize(res.Freed))

	if removeEmpty {
		// Sort candidates by depth (deepest first) so we remove children
		// before parents. After each successful removal, walk up and try
		// to remove the parent too.
		dirs := make([]string, 0, len(emptyCandidates))
		for d := range emptyCandidates {
			dirs = append(dirs, d)
		}
		sort.Slice(dirs, func(i, j int) bool {
			return strings.Count(dirs[i], string(os.PathSeparator)) >
				strings.Count(dirs[j], string(os.PathSeparator))
		})
		for _, d := range dirs {
			cur := d
			for cur != p.Root && strings.HasPrefix(cur, p.Root) {
				if err := os.Remove(cur); err != nil {
					break // not empty or other error — stop ascending
				}
				res.DirsRemoved++
				cur = filepath.Dir(cur)
			}
		}
	}
	return res
}

// PrintPlan prints a human-readable summary of the plan with a sample of
// the first `sample` sets and a per-top-level-directory breakdown.
func PrintPlan(p *Plan, sample int) {
	fmt.Printf("\nscanned %d file(s) totalling %s; %d skipped\n",
		p.TotalFiles, HumanSize(p.TotalBytes), p.Skipped)
	if len(p.Sets) == 0 {
		fmt.Println("no duplicates found")
		return
	}
	fmt.Printf("found %d duplicate file(s) in %d set(s); %s would be freed\n",
		p.TotalDups, len(p.Sets), HumanSize(p.BytesFreed))

	printBreakdown(p)

	fmt.Println()
	n := min(len(p.Sets), sample)
	for _, s := range p.Sets[:n] {
		fmt.Printf("  keep:   %s  (%s)\n", s.Keep, HumanSize(s.Size))
		for _, d := range s.Delete {
			fmt.Printf("  delete: %s\n", d)
		}
		fmt.Println()
	}
	if len(p.Sets) > sample {
		fmt.Printf("(showing %d of %d set(s))\n", sample, len(p.Sets))
	}
}

// printBreakdown prints two tables: where the keepers live, and where
// the to-be-deleted files live, grouped by top-level directory under
// the dedupe root.
func printBreakdown(p *Plan) {
	type stat struct {
		count int
		bytes int64
	}
	keep := map[string]*stat{}
	del := map[string]*stat{}
	bump := func(m map[string]*stat, key string, sz int64) {
		s, ok := m[key]
		if !ok {
			s = &stat{}
			m[key] = s
		}
		s.count++
		s.bytes += sz
	}
	for _, s := range p.Sets {
		bump(keep, topDir(s.Keep), s.Size)
		for _, d := range s.Delete {
			bump(del, topDir(d), s.Size)
		}
	}
	sortedKeys := func(m map[string]*stat) []string {
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool { return m[ks[i]].count > m[ks[j]].count })
		return ks
	}
	fmt.Println("\nkeepers by top-level directory:")
	for _, k := range sortedKeys(keep) {
		s := keep[k]
		fmt.Printf("  %6d sets  %10s  %s\n", s.count, HumanSize(s.bytes), k)
	}
	fmt.Println("to-delete by top-level directory:")
	for _, k := range sortedKeys(del) {
		s := del[k]
		fmt.Printf("  %6d files %10s  %s\n", s.count, HumanSize(s.bytes), k)
	}
}

func topDir(rel string) string {
	if before, _, ok := strings.Cut(rel, "/"); ok {
		return before
	}
	return "."
}

func HumanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
