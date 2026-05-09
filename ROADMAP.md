# Roadmap

## Near term

- Wider device support: detect non-Samsung MTP devices and let user pick when
  multiple are mounted.
- `--source-pattern` to override the `Internal storage/DCIM` subpath.
- Progress reporting summary (file count, bytes) after rsync completes.

## Maybe

- EXIF-based reorganisation hook (year/month folders) as a built-in.
- Duplicate detection against an existing photo library.
- Use `go-mtpfs` or libmtp directly instead of going through gvfs.
