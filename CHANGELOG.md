# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Initial implementation: MTP auto-detection, rsync wrapper, post-sync hooks.
- Subcommands: `sync`, `doctor`, `config init|path`, `manifest path|clear`,
  `version`.
- Pre-sync source walk with live progress: prints total files seen, how
  many are already in the manifest, and how many are new.
- Manifest of transferred files at `$XDG_STATE_HOME/photo-importer/
  transferred.txt`; subsequent syncs skip anything listed via rsync
  `--files-from`. `--no-manifest` opts out for a single run.
- Post-sync hooks now also receive `PHOTO_IMPORTER_FILES_TRANSFERRED`.
