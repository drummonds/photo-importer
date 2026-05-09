// Package config loads and writes the photo-importer YAML config.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Source MTP mount root. If empty, the first MTP mount under
	// /run/user/$UID/gvfs/ is used at runtime.
	Source string `yaml:"source"`

	// Sub-path under Source to copy from.
	SubPath string `yaml:"sub_path"`

	// Local destination directory.
	Destination string `yaml:"destination"`

	// Extra args appended to the rsync command.
	RsyncArgs []string `yaml:"rsync_args"`

	// Shell commands run after a successful sync.
	PostSync []string `yaml:"post_sync"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		SubPath:     "Internal storage/DCIM",
		Destination: filepath.Join(home, "Samsung", "Internal storage", "DCIM"),
	}
}

// Path returns the path to the config file (whether or not it exists).
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "photo-importer", "config.yaml"), nil
}

// Load reads the config file. If it does not exist, returns Default() and
// a nil error — first run is allowed without a config file.
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", p, err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", p, err)
	}
	return cfg, nil
}

// WriteDefault writes the default config to disk. Errors if a file already
// exists at the target path.
func WriteDefault() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil {
		return p, fmt.Errorf("config already exists: %s", p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return p, err
	}
	data, err := yaml.Marshal(Default())
	if err != nil {
		return p, err
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return p, err
	}
	return p, nil
}
