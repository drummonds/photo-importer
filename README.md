# photo-importer

A small Go CLI that pulls photos from an Android phone (mounted via MTP/gvfs)
to a local directory using `rsync`. Replaces a manual GRSYNC step in the
photo workflow and provides post-sync hooks for further processing.

## What it does

1. Auto-detects an MTP-mounted Android device under `/run/user/$UID/gvfs/`.
2. Verifies the device is reachable via `adb devices` (warning only).
3. Runs `rsync` with MTP-friendly options from `<mount>/Internal storage/DCIM`
   to the configured destination.
4. Runs any configured post-sync hook commands.

## Install

```sh
go install codeberg.org/hum3/photo-importer@latest
```

## Usage

```sh
photo-importer doctor          # check MTP mount, adb, destination
photo-importer config init     # write default ~/.config/photo-importer/config.yaml
photo-importer sync            # do the transfer
photo-importer sync --dry-run  # preview without copying
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

# Commands run after a successful sync. Each runs with these env vars set:
#   PHOTO_IMPORTER_SOURCE, PHOTO_IMPORTER_DEST
post_sync:
  - "echo 'sync complete:' \"$PHOTO_IMPORTER_DEST\""
```

## Workflow

- Connect phone with a data USB cable
- On the phone: notification → "USB controlled by this device" → "Use USB
  for: Transferring files"
- `photo-importer doctor` to confirm the mount appeared
- `photo-importer sync`

## Links

- Source: https://codeberg.org/hum3/photo-importer
- Mirror: https://github.com/drummonds/photo-importer
