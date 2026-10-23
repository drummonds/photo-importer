# photo-importer

A small Go CLI that pulls photos from an Android phone (mounted via MTP/gvfs)
to a local directory using `rsync`. Replaces a manual GRSYNC step in the
photo workflow and provides post-sync hooks for further processing.

## What it does

1. Auto-detects an MTP-mounted Android device under `/run/user/$UID/gvfs/`.
2. Verifies the device is reachable via `adb devices` (warning only).
3. Walks the source itself (with live progress) and reports total files
   seen, manifest skips, and how many are new.
4. Hands the new files to `rsync` via `--files-from` so it doesn't re-walk
   the slow MTP mount.
5. Records every transferred file in a manifest at
   `~/.local/state/photo-importer/transferred.txt`. Subsequent runs skip
   anything in the manifest, so files you've sorted/moved out of the
   destination won't be re-pulled.
6. Runs any configured post-sync hook commands.

## Install

```sh
go install git.bytestone.uk/hum3/photo-importer@latest
```

Later, `photo-importer self update` reinstalls the latest release.

## Usage

```sh
photo-importer doctor              # check MTP mount, adb, destination
photo-importer config init         # write default ~/.config/photo-importer/config.yaml
photo-importer sync                # do the transfer
photo-importer sync --dry-run      # preview without copying
photo-importer sync --no-manifest  # ignore the transferred-files manifest for one run
photo-importer manifest path       # print the manifest file path
photo-importer manifest clear      # forget what's been transferred

photo-importer dedupe              # dry-run: find byte-identical duplicates
photo-importer dedupe --apply      # actually delete (asks for confirmation)
photo-importer dedupe --root=DIR   # scan somewhere other than the destination

photo-importer timings             # show the last 10 sync runs
photo-importer timings path        # print the timings file path
photo-importer timings clear       # forget recorded timings
```

With no arguments, `photo-importer` runs `sync`.

## Config

`~/.config/photo-importer/config.yaml`:

```yaml
# Optional. If empty, the first MTP mount under /run/user/$UID/gvfs/ is used.
source: ""

# Subpath under the MTP mount (or under `source` if set).
sub_path: "Internal storage/DCIM"

# Destination directory on the local filesystem.
destination: "/home/hum3/Samsung/Internal storage/DCIM"

# Extra rsync arguments appended to the defaults.
rsync_args: []

# Commands run after a successful sync. Each runs via `sh -c` with these
# env vars exported:
#   PHOTO_IMPORTER_SOURCE
#   PHOTO_IMPORTER_DEST
#   PHOTO_IMPORTER_FILES_TRANSFERRED
post_sync:
  - 'echo "sync complete: $PHOTO_IMPORTER_FILES_TRANSFERRED files into $PHOTO_IMPORTER_DEST"'
```

## Dedupe

`dedupe` finds byte-identical duplicate files anywhere under the
destination tree. It's intended for cleaning up artifacts of botched
rsync runs (e.g. a `DCIM/DCIM/...` subtree that mirrors `DCIM/...`).

For each duplicate set the canonical keeper is the shallowest path
(tie-broken lexicographically). Default is dry-run; `--apply` deletes
after a y/N prompt (`--yes` skips the prompt). Empty directories are
removed afterwards unless `--rm-empty=false`.

Files are first grouped by size — only files sharing a size with
another file are hashed (SHA-256), so most of a 100 GB tree is read
once at most.

## Live ETA + timings history

Each successful (or failed) sync is appended to a JSONL file at
`$XDG_STATE_HOME/photo-importer/timings.jsonl`, capped at the most
recent 10 runs. One JSON object per line; trivial to parse with `jq`:

```sh
jq -r '"\(.start) — \(.duration_ms/1000)s — seen=\(.files_seen)"' \
   ~/.local/state/photo-importer/timings.jsonl
```

Each Run records the start/end times, success flag, files seen vs
transferred, and a per-phase breakdown in milliseconds:
`load_manifest`, `scan_source`, `rsync`, `update_manifest`, `hooks`.

During every sync, a `[eta]` line is printed to stderr every 10
seconds while a phase is in progress. It uses the average phase
durations from past runs to estimate remaining time. With no history
yet (first run), the banner shows elapsed time only.

```
[eta] phase=rsync  phase-elapsed=20s  total-elapsed=22s  remaining=~3m40s total (~3m20s in current phase)  next-update=10s
```

## Workflow

- Connect phone with a data USB cable
- On the phone: notification → "USB controlled by this device" → "Use USB
  for: Transferring files"
- `photo-importer doctor` to confirm the mount appeared
- `photo-importer sync`

## Links

- Source: https://git.bytestone.uk/hum3/photo-importer
- Mirror: https://github.com/drummonds/photo-importer
