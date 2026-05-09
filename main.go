// photo-importer pulls photos from a gvfs-mounted MTP Android device to a
// local directory using rsync, with optional post-sync hooks.
package main

import (
	"flag"
	"fmt"
	"os"

	"codeberg.org/hum3/photo-importer/internal/config"
	"codeberg.org/hum3/photo-importer/internal/importer"
	"codeberg.org/hum3/photo-importer/internal/manifest"
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
	fmt.Fprintf(w, `photo-importer %s

Usage:
  photo-importer [sync] [--dry-run] [-v] [--no-manifest]
  photo-importer doctor
  photo-importer config init|path
  photo-importer manifest path|clear
  photo-importer version
`, version)
}

func runSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would be transferred without copying")
	verbose := fs.Bool("v", false, "verbose rsync output")
	noManifest := fs.Bool("no-manifest", false, "ignore the transferred-files manifest for this run")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	if err := importer.Sync(cfg, importer.Options{DryRun: *dryRun, Verbose: *verbose, NoManifest: *noManifest}); err != nil {
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
