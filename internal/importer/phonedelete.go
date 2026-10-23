package importer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"git.bytestone.uk/hum3/photo-importer/internal/deletions"
)

// phoneDeletionResult counts what applyPhoneDeletions did with the queue.
type phoneDeletionResult struct {
	Present int // queued and still on the phone
	Absent  int // queued but already gone from the phone (dequeued)
	Deleted int // removed from the phone this run (dequeued)
	Failed  int // removal failed (still queued)
	Skipped int // present but the user declined (still queued)
}

func say(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// applyPhoneDeletions removes queued files from the phone. Entries already
// absent from the phone are dropped from the queue without asking. Files
// still present are deleted only if confirm returns true; confirm receives
// the count and a short preview of paths. In dry-run mode nothing is
// deleted and the queue is left untouched.
func applyPhoneDeletions(source string, q *deletions.Queue, dryRun bool, confirm func(n int, preview []string) bool, out io.Writer) phoneDeletionResult {
	var res phoneDeletionResult
	if q.Len() == 0 {
		return res
	}
	var present, absent []string
	for _, rel := range q.Paths() {
		if _, err := os.Stat(filepath.Join(source, filepath.FromSlash(rel))); err == nil {
			present = append(present, rel)
		} else {
			absent = append(absent, rel)
		}
	}
	res.Present, res.Absent = len(present), len(absent)
	say(out, "deletion queue: %d file(s) queued for deletion from the phone (%d present, %d already gone)\n",
		q.Len(), len(present), len(absent))
	if dryRun {
		say(out, "(dry-run: nothing deleted from the phone, queue untouched)\n")
		return res
	}
	if len(absent) > 0 {
		if err := q.Remove(absent); err != nil {
			say(out, "warn: dequeue absent files: %v\n", err)
		}
	}
	if len(present) == 0 {
		return res
	}
	const previewLimit = 20
	preview := present[:min(len(present), previewLimit)]
	if confirm == nil || !confirm(len(present), preview) {
		res.Skipped = len(present)
		say(out, "phone deletions skipped; %d file(s) remain queued\n", len(present))
		return res
	}
	var done []string
	for _, rel := range present {
		if err := os.Remove(filepath.Join(source, filepath.FromSlash(rel))); err != nil {
			say(out, "  warn: delete %s: %v\n", rel, err)
			res.Failed++
			continue
		}
		done = append(done, rel)
	}
	res.Deleted = len(done)
	if err := q.Remove(done); err != nil {
		say(out, "warn: update deletion queue: %v\n", err)
	}
	say(out, "deleted %d file(s) from the phone; %d failed (still queued)\n", res.Deleted, res.Failed)
	return res
}
