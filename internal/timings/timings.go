// Package timings persists the duration of each sync run (and its
// phases) to an append-only JSONL file. Each line is one Run object.
// The file is truncated to the most recent MaxRuns entries on every
// Append, so it stays small and easy to parse with `jq` etc.
package timings

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// MaxRuns is the number of historical runs we keep in the file.
const MaxRuns = 10

// Run records one sync invocation.
type Run struct {
	Start            time.Time        `json:"start"`
	End              time.Time        `json:"end"`
	DurationMs       int64            `json:"duration_ms"`
	Success          bool             `json:"success"`
	FilesSeen        int              `json:"files_seen"`
	FilesTransferred int              `json:"files_transferred"`
	// Phases maps phase name → milliseconds spent in that phase.
	Phases map[string]int64 `json:"phases"`
}

// Phase names. Keep stable — the predictor groups history by these.
const (
	PhaseLoadManifest   = "load_manifest"
	PhaseScanSource     = "scan_source"
	PhaseRsync          = "rsync"
	PhaseUpdateManifest = "update_manifest"
	PhaseHooks          = "hooks"
)

// Path returns the JSONL path under $XDG_STATE_HOME (or ~/.local/state).
func Path() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "photo-importer", "timings.jsonl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "photo-importer", "timings.jsonl"), nil
}

// Load reads all runs from the file, oldest first. Returns an empty
// slice if the file does not exist.
func Load() ([]Run, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []Run
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Run
		if err := json.Unmarshal(line, &r); err != nil {
			// Skip unparseable lines rather than fail the whole load.
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Start.Before(runs[j].Start) })
	return runs, sc.Err()
}

// Append writes r and trims the file to the most recent MaxRuns entries.
func Append(r Run) error {
	runs, _ := Load() // best-effort; treat unreadable as empty
	runs = append(runs, r)
	if len(runs) > MaxRuns {
		runs = runs[len(runs)-MaxRuns:]
	}
	return writeAll(runs)
}

func writeAll(runs []Run) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "timings-*.jsonl.tmp")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	for _, r := range runs {
		if err := enc.Encode(r); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// AveragePhases returns mean ms-per-phase across the runs (only successful
// runs are considered). Phases missing from a run contribute zero.
func AveragePhases(runs []Run) map[string]time.Duration {
	if len(runs) == 0 {
		return nil
	}
	sum := map[string]int64{}
	count := 0
	for _, r := range runs {
		if !r.Success {
			continue
		}
		count++
		for k, v := range r.Phases {
			sum[k] += v
		}
	}
	if count == 0 {
		return nil
	}
	out := make(map[string]time.Duration, len(sum))
	for k, v := range sum {
		out[k] = time.Duration(v/int64(count)) * time.Millisecond
	}
	return out
}

// AverageRsyncPerFile returns the mean rsync duration per transferred
// file across successful runs. Useful for predicting rsync ETA when the
// number of pending files is known but past runs varied in count.
// Returns 0 if there is no usable data.
func AverageRsyncPerFile(runs []Run) time.Duration {
	var totalMs int64
	var totalFiles int
	for _, r := range runs {
		if !r.Success || r.FilesTransferred <= 0 {
			continue
		}
		ms, ok := r.Phases[PhaseRsync]
		if !ok || ms <= 0 {
			continue
		}
		totalMs += ms
		totalFiles += r.FilesTransferred
	}
	if totalFiles == 0 {
		return 0
	}
	return time.Duration(totalMs/int64(totalFiles)) * time.Millisecond
}
