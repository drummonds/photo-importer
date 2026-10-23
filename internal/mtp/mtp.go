// Package mtp discovers gvfs MTP mounts for connected Android devices.
package mtp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Mount is a single gvfs MTP mountpoint.
type Mount struct {
	Path string // e.g. /run/user/1000/gvfs/mtp:host=SAMSUNG_..._R5CR7028Y9R
	Host string // the bit after "mtp:host="
}

// Discover returns all currently mounted MTP devices for the current user.
func Discover() ([]Mount, error) {
	base := fmt.Sprintf("/run/user/%d/gvfs", os.Getuid())
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Mount
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "mtp:host=") {
			continue
		}
		out = append(out, Mount{
			Path: filepath.Join(base, name),
			Host: strings.TrimPrefix(name, "mtp:host="),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}
