# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.3.0] - 2026-10-23

 - `photo-importer edit`: a local web editor over the destination; tick photos and delete them. Deletions are queued and removed from the phone on the next `sync` after it reports the count and asks (`--yes` skips the prompt). `deletions list|path|clear` inspects the queue.

### Added

- `photo-importer edit`: a local web editor over the destination; tick photos and delete them. Deletions are queued and removed from the phone on the next `sync` after it reports the count and asks (`--yes` skips the prompt). `deletions list|path|clear` inspects the queue. (#15)

## [0.2.0] - 2026-10-23

 - On KDE, `sync` and `doctor` acquire the MTP mount themselves: when gvfs lists the phone as a volume but kiod6's kmtpd holds the device, they quit kiod6 (it restarts on demand) and mount via `gio`. GNOME's plug-in gvfs mount is used as before.

### Added

- On KDE, `sync` and `doctor` acquire the MTP mount themselves: when gvfs lists the phone as a volume but kiod6's kmtpd holds the device, they quit kiod6 (it restarts on demand) and mount via `gio`. GNOME's plug-in gvfs mount is used as before. (#12)

## [0.1.0] - 2026-10-23

 - `self update` subcommand reinstalls the latest release from the Go module proxy; project adopts task-plus conventions (golangci-lint in `check`, Go 1.27.3, goreleaser for Forgejo, roadmap as issues, code level 1).

### Added

- Initial implementation: MTP auto-detection, rsync wrapper, post-sync hooks.
  - Subcommands: `sync`, `doctor`, `config init|path`, `manifest path|clear`, `version`.
  - Pre-sync source walk with live progress: prints total files seen, how many are already in the manifest, and how many are new.
  - Manifest of transferred files at `$XDG_STATE_HOME/photo-importer/transferred.txt`; subsequent syncs skip anything listed via rsync `--files-from`. `--no-manifest` opts out for a single run.
  - Post-sync hooks also receive `PHOTO_IMPORTER_FILES_TRANSFERRED`.
  - `dedupe` subcommand: walks the destination, identifies byte-identical duplicate files (size-grouping then SHA-256), and proposes to delete all but the shallowest copy. Dry-run by default; `--apply` deletes after confirmation, `--rm-empty` cleans up emptied directories.
  - Per-run timings recorded as JSONL at `$XDG_STATE_HOME/photo-importer/timings.jsonl` (capped at the most recent 10 runs): start/end times, success flag, file counts, and a per-phase ms breakdown for `load_manifest`, `scan_source`, `rsync`, `update_manifest`, `hooks`.
  - During sync, an `[eta]` banner is printed to stderr every 10 seconds with the current phase, elapsed time, and predicted remaining time derived from history.
  - `timings` subcommand: `show` (default) prints a table of the last 10 runs; `path` and `clear` are also available. (#7)
- `self update` subcommand reinstalls the latest release from the Go module proxy; project adopts task-plus conventions (golangci-lint in `check`, Go 1.27.3, goreleaser for Forgejo, roadmap as issues, code level 1). (#8)

