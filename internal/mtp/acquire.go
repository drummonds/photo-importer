package mtp

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Acquirer obtains a gvfs MTP mount for a connected phone. gvfs is the
// primary path: on a GNOME desktop the phone is mounted on plug-in and
// Discover finds it. On KDE, kiod6's kmtpd module claims the MTP interface
// first, so gvfs lists the phone as a volume but cannot mount it. Acquire
// resolves that by quitting kiod6 (it restarts on demand) and mounting via
// gio.
type Acquirer struct {
	Discover func() ([]Mount, error)                           // gvfs mounts present now
	Run      func(name string, args ...string) (string, error) // runs a command, returning combined output
	Notice   io.Writer                                         // where to say what was done on the user's behalf
	Retry    time.Duration                                     // pause between mount attempts after releasing kiod6
}

// Acquire returns the first gvfs MTP mount, mounting the phone if needed.
//
//	gvfs mount present | MTP volume listed | gio mount   | kiod6 running | outcome
//	yes                | -                 | -           | -             | use it
//	no                 | no                | -           | -             | error: not in file-transfer mode
//	no                 | yes               | ok          | -             | use the new mount
//	no                 | yes               | device busy | yes           | quit kiod6, retry, use the mount
//	no                 | yes               | device busy | no            | error: device busy
//	no                 | yes               | other error | -             | that error
func (a Acquirer) Acquire() (Mount, error) {
	if m, ok, err := a.first(); err != nil || ok {
		return m, err
	}
	listing, _ := a.Run("gio", "mount", "-li")
	roots := mtpVolumes(listing)
	if len(roots) == 0 {
		return Mount{}, fmt.Errorf("no MTP device found — connect phone and set USB to file transfer mode")
	}
	root := roots[0]
	out, err := a.Run("gio", "mount", root)
	if err == nil {
		return a.mounted(root)
	}
	if !isBusy(out) {
		return Mount{}, fmt.Errorf("gio mount %s: %s", root, firstLine(out, err))
	}
	if _, qerr := a.Run("kquitapp6", "kiod6"); qerr != nil {
		return Mount{}, fmt.Errorf("MTP device busy: another process holds %s — %s", root, firstLine(out, err))
	}
	_, _ = fmt.Fprintln(a.Notice, "released KDE's hold on the phone (kquitapp6 kiod6) so gvfs can mount it")
	for attempt := 0; ; attempt++ {
		out, err = a.Run("gio", "mount", root)
		if err == nil {
			return a.mounted(root)
		}
		if !isBusy(out) || attempt >= 4 {
			return Mount{}, fmt.Errorf("gio mount %s after quitting kiod6: %s", root, firstLine(out, err))
		}
		time.Sleep(a.Retry)
	}
}

func (a Acquirer) first() (Mount, bool, error) {
	mounts, err := a.Discover()
	if err != nil || len(mounts) == 0 {
		return Mount{}, false, err
	}
	return mounts[0], true, nil
}

func (a Acquirer) mounted(root string) (Mount, error) {
	m, ok, err := a.first()
	if err != nil {
		return Mount{}, err
	}
	if !ok {
		return Mount{}, fmt.Errorf("gio mounted %s but nothing appeared under gvfs", root)
	}
	return m, nil
}

// mtpVolumes returns the activation roots of the MTP volumes in the output
// of `gio mount -li`.
func mtpVolumes(listing string) []string {
	var roots []string
	inMTP := false
	for line := range strings.SplitSeq(listing, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Volume("):
			inMTP = false
		case strings.HasPrefix(trimmed, "Type:") && strings.Contains(trimmed, "GProxyVolumeMonitorMTP"):
			inMTP = true
		case inMTP && strings.HasPrefix(trimmed, "activation_root=mtp://"):
			roots = append(roots, strings.TrimPrefix(trimmed, "activation_root="))
		}
	}
	return roots
}

func isBusy(gioOutput string) bool {
	return strings.Contains(gioOutput, "Unable to open MTP device")
}

func firstLine(out string, err error) string {
	if l := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]); l != "" {
		return l
	}
	return err.Error()
}

// Acquire uses the real gvfs directory and the real commands, reporting
// anything done on the user's behalf to notice.
func Acquire(notice io.Writer) (Mount, error) {
	return Acquirer{
		Discover: Discover,
		Run: func(name string, args ...string) (string, error) {
			b, err := exec.Command(name, args...).CombinedOutput()
			return string(b), err
		},
		Notice: notice,
		Retry:  500 * time.Millisecond,
	}.Acquire()
}
