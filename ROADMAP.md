# Roadmap

## Near term

Next up once the current import flow is stable.
- #1 Wider device support: detect non-Samsung MTP devices and let user pick when multiple are mounted
- #2 Progress reporting summary (file count, bytes) after rsync completes
- #6 --source-pattern to override the Internal storage/DCIM subpath

## Maybe

Ideas worth keeping that nothing yet depends on.
- #3 EXIF-based reorganisation hook (year/month folders) as a built-in
- #4 Duplicate detection against an existing photo library
- #5 Use go-mtpfs or libmtp directly instead of going through gvfs
