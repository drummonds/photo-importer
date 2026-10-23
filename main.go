// photo-importer pulls photos from a gvfs-mounted MTP Android device to a
// local directory using rsync, with optional post-sync hooks.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"git.bytestone.uk/hum3/photo-importer/internal/config"
	"git.bytestone.uk/hum3/photo-importer/internal/dedupe"
	"git.bytestone.uk/hum3/photo-importer/internal/deletions"
	"git.bytestone.uk/hum3/photo-importer/internal/editor"
	"git.bytestone.uk/hum3/photo-importer/internal/importer"
	"git.bytestone.uk/hum3/photo-importer/internal/manifest"
	"git.bytestone.uk/hum3/photo-importer/internal/self"
	"git.bytestone.uk/hum3/photo-importer/internal/timings"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		os.Exit(runSync(nil))
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "sync":
		os.Exit(runSync(args))
	case "doctor":
		os.Exit(runDoctor())
	case "config":
		os.Exit(runConfig(args))
	case "manifest":
		os.Exit(runManifest(args))
	case "dedupe":
		os.Exit(runDedupe(args))
	case "edit":
		os.Exit(runEdit(args))
	case "deletions":
		os.Exit(runDeletions(args))
	case "timings":
		os.Exit(runTimings(args))
	case "self":
		if err := self.Run(args, version); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "version", "-V", "--version":
		fmt.Println("photo-importer", version)
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	_, _ = fmt.Fprintf(w, `photo-importer %s

Usage:
  photo-importer [sync] [--dry-run] [-v] [--no-manifest] [--yes]
  photo-importer doctor
  photo-importer config init|path
  photo-importer manifest path|clear
  photo-importer edit [--addr=127.0.0.1:1340] [--open]
  photo-importer deletions [list|path|clear]
  photo-importer dedupe [--apply] [--rm-empty] [--root=DIR] [--yes]
  photo-importer timings [show|path|clear]
  photo-importer self update
  photo-importer version
`, version)
}

func runSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would be transferred without copying")
	verbose := fs.Bool("v", false, "verbose rsync output")
	noManifest := fs.Bool("no-manifest", false, "ignore the transferred-files manifest for this run")
	yes := fs.Bool("yes", false, "delete queued files from the phone without prompting")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	confirm := func(n int, preview []string) bool {
		for _, p := range preview {
			fmt.Println("  ", p)
		}
		if n > len(preview) {
			fmt.Printf("   ... and %d more\n", n-len(preview))
		}
		if *yes {
			return true
		}
		return promptYes(fmt.Sprintf("Delete %d file(s) from the phone? [y/N] ", n))
	}
	opts := importer.Options{DryRun: *dryRun, Verbose: *verbose, NoManifest: *noManifest, ConfirmPhoneDeletions: confirm}
	if err := importer.Sync(cfg, opts); err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
		return 1
	}
	return 0
}

func runDoctor() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	checks := importer.Doctor(cfg)
	failed := 0
	for _, c := range checks {
		mark := "OK "
		if !c.OK {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %-12s %s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\n%d check(s) failed\n", failed)
		return 1
	}
	return 0
}

func runConfig(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println("usage: photo-importer config init")
		return 2
	}
	switch args[0] {
	case "init":
		p, err := config.WriteDefault()
		if err != nil {
			fmt.Fprintln(os.Stderr, "config init:", err)
			return 1
		}
		fmt.Println("wrote", p)
		return 0
	case "path":
		p, err := config.Path()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(p)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand: %s\n", args[0])
		return 2
	}
}

func runDedupe(args []string) int {
	fs := flag.NewFlagSet("dedupe", flag.ExitOnError)
	apply := fs.Bool("apply", false, "actually delete duplicate files (default: dry-run)")
	rmEmpty := fs.Bool("rm-empty", true, "remove emptied directories after delete")
	rootFlag := fs.String("root", "", "directory to scan (default: destination from config)")
	yes := fs.Bool("yes", false, "skip confirmation prompt when --apply")
	sample := fs.Int("sample", 10, "number of duplicate sets to print as a preview")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	root := *rootFlag
	if root == "" {
		root = cfg.Destination
	}
	if root == "" {
		fmt.Fprintln(os.Stderr, "no root: pass --root or set destination in config")
		return 1
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "root not a readable directory: %s\n", root)
		return 1
	}

	plan, err := dedupe.Build(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dedupe:", err)
		return 1
	}
	dedupe.PrintPlan(plan, *sample)

	if !*apply {
		if len(plan.Sets) > 0 {
			fmt.Println("(dry-run — pass --apply to delete)")
		}
		return 0
	}
	if len(plan.Sets) == 0 {
		return 0
	}
	if !*yes && !promptYes(fmt.Sprintf("\nDelete %d file(s), freeing %s? [y/N] ", plan.TotalDups, dedupe.HumanSize(plan.BytesFreed))) {
		fmt.Println("aborted")
		return 1
	}
	res := dedupe.Apply(plan, *rmEmpty)
	fmt.Printf("deleted %d file(s), freed %s, removed %d empty dir(s), errors: %d\n",
		res.Deleted, dedupe.HumanSize(res.Freed), res.DirsRemoved, res.Errors)
	if res.Errors > 0 {
		return 1
	}
	return 0
}

func runTimings(args []string) int {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "show", "":
		runs, err := timings.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if len(runs) == 0 {
			fmt.Println("no recorded runs")
			return 0
		}
		fmt.Printf("%-20s  %-8s  %-3s  %7s  %7s  %s\n",
			"start", "duration", "ok", "seen", "xferred", "phases (ms)")
		for _, r := range runs {
			ok := "✓"
			if !r.Success {
				ok = "✗"
			}
			fmt.Printf("%-20s  %-8s  %-3s  %7d  %7d  %s\n",
				r.Start.Local().Format("2006-01-02 15:04:05"),
				time.Duration(r.DurationMs*int64(time.Millisecond)).Round(time.Second),
				ok, r.FilesSeen, r.FilesTransferred, formatPhases(r.Phases))
		}
		return 0
	case "path":
		p, err := timings.Path()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(p)
		return 0
	case "clear":
		p, err := timings.Path()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("cleared", p)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown timings subcommand: %s\n", sub)
		return 2
	}
}

func formatPhases(p map[string]int64) string {
	parts := []string{}
	for _, k := range timings.PhaseOrder {
		if v, ok := p[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%d", k, v))
		}
	}
	return strings.Join(parts, " ")
}

func runManifest(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println("usage: photo-importer manifest path|clear")
		return 2
	}
	switch args[0] {
	case "path":
		p, err := manifest.Path()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(p)
		return 0
	case "clear":
		p, err := manifest.Path()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("cleared", p)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown manifest subcommand: %s\n", args[0])
		return 2
	}
}

// promptYes prints prompt and reads one line from stdin; only "y"/"yes" is true.
func promptYes(prompt string) bool {
	fmt.Print(prompt)
	ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))
	return ans == "y" || ans == "yes"
}

func runEdit(args []string) int {
	fs := flag.NewFlagSet("edit", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:1340", "listen address")
	open := fs.Bool("open", false, "open the editor in the default browser (xdg-open)")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	if cfg.Destination == "" {
		fmt.Fprintln(os.Stderr, "destination is empty — run `photo-importer config init` and edit the file")
		return 1
	}
	q, err := deletions.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deletion queue:", err)
		return 1
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ed, err := editor.New(cfg.Destination, q, filepath.Join(cacheRoot, "photo-importer", "thumbs"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "edit:", err)
		return 1
	}
	url := "http://" + *addr + "/"
	fmt.Printf("editing %s\nopen %s (Ctrl-C to stop)\n", cfg.Destination, url)
	if *open {
		if err := exec.Command("xdg-open", url).Start(); err != nil {
			fmt.Fprintln(os.Stderr, "warn: xdg-open:", err)
		}
	}
	if err := http.ListenAndServe(*addr, ed.Handler()); err != nil { //nolint:gosec // local tool, no timeouts needed
		fmt.Fprintln(os.Stderr, "edit:", err)
		return 1
	}
	return 0
}

func runDeletions(args []string) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		q, err := deletions.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if q.Len() == 0 {
			fmt.Println("deletion queue is empty")
			return 0
		}
		for _, p := range q.Paths() {
			fmt.Println(p)
		}
		fmt.Fprintf(os.Stderr, "%d file(s) queued for deletion from the phone on the next sync\n", q.Len())
		return 0
	case "path":
		p, err := deletions.Path()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(p)
		return 0
	case "clear":
		p, err := deletions.Path()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("cleared", p)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown deletions subcommand: %s\n", sub)
		return 2
	}
}
