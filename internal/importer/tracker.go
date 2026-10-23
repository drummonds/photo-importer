package importer

import (
	"fmt"
	"maps"
	"os"
	"sync"
	"time"

	"git.bytestone.uk/hum3/photo-importer/internal/timings"
)

// tickInterval is how often the live ETA banner is printed.
const tickInterval = 10 * time.Second

// tracker measures phase durations of a single sync run and prints a
// live `[eta] phase=... elapsed=... remaining=...` banner every
// tickInterval seconds, using past runs to estimate remaining time.
type tracker struct {
	history   []timings.Run
	startedAt time.Time

	mu          sync.Mutex
	phaseName   string
	phaseStart  time.Time
	completed   map[string]time.Duration
	pending     int // for rsync ETA refinement
	filesSeen   int // for scan ETA refinement
	transferred int // for the final Run record

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func newTracker(history []timings.Run) *tracker {
	return &tracker{
		history:   history,
		startedAt: time.Now(),
		completed: map[string]time.Duration{},
		stopCh:    make(chan struct{}),
	}
}

// start spawns the background ticker goroutine. Call stop when done.
func (t *tracker) start() {
	t.wg.Add(1)
	go t.tickLoop()
}

func (t *tracker) stop() {
	close(t.stopCh)
	t.wg.Wait()
}

// phase marks the start of a new phase, recording the duration of the
// previous one (if any).
func (t *tracker) phase(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if t.phaseName != "" {
		t.completed[t.phaseName] = now.Sub(t.phaseStart)
	}
	t.phaseName = name
	t.phaseStart = now
}

// endPhase records the current phase as finished without starting a
// new one. Used right before stop().
func (t *tracker) endPhase() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.phaseName != "" {
		t.completed[t.phaseName] = time.Since(t.phaseStart)
		t.phaseName = ""
	}
}

func (t *tracker) setScanProgress(filesSeen int) {
	t.mu.Lock()
	t.filesSeen = filesSeen
	t.mu.Unlock()
}

func (t *tracker) setPending(n int) {
	t.mu.Lock()
	t.pending = n
	t.mu.Unlock()
}

func (t *tracker) setTransferred(n int) {
	t.mu.Lock()
	t.transferred = n
	t.mu.Unlock()
}

func (t *tracker) tickLoop() {
	defer t.wg.Done()
	tk := time.NewTicker(tickInterval)
	defer tk.Stop()
	for {
		select {
		case <-t.stopCh:
			return
		case <-tk.C:
			t.printBanner()
		}
	}
}

func (t *tracker) printBanner() {
	t.mu.Lock()
	phase := t.phaseName
	if phase == "" {
		t.mu.Unlock()
		return
	}
	phaseElapsed := time.Since(t.phaseStart)
	totalElapsed := time.Since(t.startedAt)
	pending := t.pending
	filesSeen := t.filesSeen
	completed := maps.Clone(t.completed)
	t.mu.Unlock()
	_ = completed // reserved for future per-phase delta logic

	phaseRem, totalRem := t.estimate(phase, phaseElapsed, pending, filesSeen)

	// Ticker prints to stderr (separate from stdout's rsync progress
	// or scan transient). Leading \n ensures we start on a fresh line
	// even if a \r-overwriting status line is mid-write on stdout.
	fmt.Fprintf(os.Stderr, "\n[eta] phase=%s  phase-elapsed=%s  total-elapsed=%s  remaining=%s  next-update=10s\n",
		phase, fmtDur(phaseElapsed), fmtDur(totalElapsed), fmtRemaining(totalRem, phaseRem))
}

// estimate returns (remaining for current phase, remaining for whole
// run). Returns (0, 0) when there's no history to estimate from.
func (t *tracker) estimate(currentPhase string, currentElapsed time.Duration, pending, filesSeen int) (phase, total time.Duration) {
	avg := timings.AveragePhases(t.history)
	if len(avg) == 0 {
		return 0, 0
	}

	// Phase remaining: by default avg(phase) - elapsed.
	phaseEst := avg[currentPhase]

	// Refinement for rsync: per-file rate × pending if both known.
	if currentPhase == timings.PhaseRsync && pending > 0 {
		perFile := timings.AverageRsyncPerFile(t.history)
		if perFile > 0 {
			est := time.Duration(pending) * perFile
			phaseEst = est
		}
	}
	// (Previously this scaled scan estimate by ratio of files-seen to
	// past total. That blew up when MTP was slow to wake — 1 file seen
	// in 10s extrapolated to ~200h. Sticking with the flat average is
	// more useful in practice; scans are short relative to rsync.)
	_ = filesSeen

	if phaseEst > currentElapsed {
		phase = phaseEst - currentElapsed
	}

	// Total remaining = phase remaining + sum of avg for phases not yet
	// completed and not the current one.
	total = phase
	order := timings.PhaseOrder
	currentIdx := -1
	for i, p := range order {
		if p == currentPhase {
			currentIdx = i
			break
		}
	}
	for i, p := range order {
		if i <= currentIdx {
			continue
		}
		total += avg[p]
	}
	return phase, total
}

// snapshot returns the Run record for persistence.
func (t *tracker) snapshot(success bool, filesSeen int) timings.Run {
	t.endPhase()
	end := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	phases := make(map[string]int64, len(t.completed))
	for k, v := range t.completed {
		phases[k] = v.Milliseconds()
	}
	return timings.Run{
		Start:            t.startedAt,
		End:              end,
		DurationMs:       end.Sub(t.startedAt).Milliseconds(),
		Success:          success,
		FilesSeen:        filesSeen,
		FilesTransferred: t.transferred,
		Phases:           phases,
	}
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func fmtRemaining(total, phase time.Duration) string {
	if total == 0 && phase == 0 {
		return "unknown (no history yet)"
	}
	return fmt.Sprintf("~%s total (~%s in current phase)", fmtDur(total), fmtDur(phase))
}
