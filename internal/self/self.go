// Package self manages the photo-importer binary itself: "self update"
// reinstalls the latest release from the Go module proxy.
package self

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	moduleName = "git.bytestone.uk/hum3/photo-importer"
	proxyURL   = "https://proxy.golang.org"
)

// Run dispatches self subcommands.
func Run(args []string, version string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: photo-importer self update")
	}
	switch args[0] {
	case "update":
		return update(version)
	default:
		return fmt.Errorf("unknown self command: %s", args[0])
	}
}

func update(current string) error {
	fmt.Printf("Current version: %s\n", current)
	latest, err := latestVersion(proxyURL)
	if err != nil {
		fmt.Printf("Could not check latest version: %v\n", err)
		fmt.Println("Proceeding with update anyway...")
	} else {
		fmt.Printf("Latest version:  %s\n", latest)
		if upToDate(current, latest) {
			fmt.Println("Already up to date.")
			return nil
		}
	}

	fmt.Printf("Installing: go install %s@latest\n", moduleName)
	cmd := exec.Command("go", "install", moduleName+"@latest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install: %w", err)
	}
	fmt.Println("Updated successfully.")
	return nil
}

// upToDate compares versions ignoring a leading "v": the binary embeds
// "0.1.0" while the proxy reports "v0.1.0".
func upToDate(current, latest string) bool {
	return strings.TrimPrefix(current, "v") == strings.TrimPrefix(latest, "v")
}

// latestVersion asks the module proxy at base for the newest version.
func latestVersion(base string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+moduleName+"/@latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("proxy returned %s", resp.Status)
	}
	var info struct{ Version string }
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("decode proxy response: %w", err)
	}
	if info.Version == "" {
		return "", fmt.Errorf("proxy response has no Version")
	}
	return info.Version, nil
}
