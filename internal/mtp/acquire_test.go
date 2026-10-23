package mtp

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const gioListing = `Drive(0): WDC WDS100T2B0C-00PXH0
  Type: GProxyDrive (GProxyVolumeMonitorUDisks2)
Volume(0): SAMSUNG Android
  Type: GProxyVolume (GProxyVolumeMonitorMTP)
  ids:
   unix-device: '/dev/bus/usb/006/004'
  activation_root=mtp://SAMSUNG_SAMSUNG_Android_R5CR7028Y9R/
  themed icons:  [multimedia-player]
  can_mount=1
  can_eject=0
Volume(1): 32 GB Volume
  Type: GProxyVolume (GProxyVolumeMonitorUDisks2)
  activation_root=file:///media/hum3/stick
`

func TestMTPVolumesFindsOnlyMTPActivationRoots(t *testing.T) {
	got := mtpVolumes(gioListing)
	want := []string{"mtp://SAMSUNG_SAMSUNG_Android_R5CR7028Y9R/"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("mtpVolumes = %v, want %v", got, want)
	}
}

func TestMTPVolumesEmptyWhenNoPhone(t *testing.T) {
	if got := mtpVolumes("Drive(0): disk\n  Type: GProxyDrive (GProxyVolumeMonitorUDisks2)\n"); len(got) != 0 {
		t.Errorf("mtpVolumes = %v, want none", got)
	}
}

// fakeRunner scripts the outcome of each external command and records the
// calls in order.
type fakeRunner struct {
	calls   []string
	outputs map[string][]result // keyed by command line; popped in order
}

type result struct {
	out string
	err error
}

func (f *fakeRunner) run(name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	q := f.outputs[line]
	if len(q) == 0 {
		return "", fmt.Errorf("unexpected command %q", line)
	}
	r := q[0]
	f.outputs[line] = q[1:]
	return r.out, r.err
}

// gvfsSequence returns a Discover that yields each listed result in turn,
// repeating the last one.
func gvfsSequence(results ...[]Mount) func() ([]Mount, error) {
	i := 0
	return func() ([]Mount, error) {
		r := results[i]
		if i < len(results)-1 {
			i++
		}
		return r, nil
	}
}

var (
	phone    = Mount{Path: "/run/user/1000/gvfs/mtp:host=SAMSUNG_SAMSUNG_Android_R5CR7028Y9R", Host: "SAMSUNG_SAMSUNG_Android_R5CR7028Y9R"}
	mountCmd = "gio mount mtp://SAMSUNG_SAMSUNG_Android_R5CR7028Y9R/"
	busy     = result{out: "gio: mtp://SAMSUNG_SAMSUNG_Android_R5CR7028Y9R/: Unable to open MTP device “006,004”\n", err: errors.New("exit status 1")}
)

func TestAcquire(t *testing.T) {
	cases := []struct {
		name      string
		gvfs      func() ([]Mount, error)
		outputs   map[string][]result
		wantCalls []string
		wantErr   string
		wantNote  string
	}{
		{
			name:      "gvfs mount already present: nothing is run",
			gvfs:      gvfsSequence([]Mount{phone}),
			wantCalls: nil,
		},
		{
			name:      "no MTP volume listed: phone is not in file-transfer mode",
			gvfs:      gvfsSequence(nil),
			outputs:   map[string][]result{"gio mount -li": {{out: "Drive(0): disk\n"}}},
			wantCalls: []string{"gio mount -li"},
			wantErr:   "file transfer",
		},
		{
			name: "volume listed and gio mounts it",
			gvfs: gvfsSequence(nil, []Mount{phone}),
			outputs: map[string][]result{
				"gio mount -li": {{out: gioListing}},
				mountCmd:        {{}},
			},
			wantCalls: []string{"gio mount -li", mountCmd},
		},
		{
			name: "device busy and kiod6 running: release KDE's hold and retry",
			gvfs: gvfsSequence(nil, []Mount{phone}),
			outputs: map[string][]result{
				"gio mount -li":   {{out: gioListing}},
				mountCmd:          {busy, {}},
				"kquitapp6 kiod6": {{}},
			},
			wantCalls: []string{"gio mount -li", mountCmd, "kquitapp6 kiod6", mountCmd},
			wantNote:  "kiod6",
		},
		{
			name: "device busy and kiod6 not running: report the busy device",
			gvfs: gvfsSequence(nil),
			outputs: map[string][]result{
				"gio mount -li":   {{out: gioListing}},
				mountCmd:          {busy},
				"kquitapp6 kiod6": {{out: "kiod6: not running", err: errors.New("exit status 1")}},
			},
			wantCalls: []string{"gio mount -li", mountCmd, "kquitapp6 kiod6"},
			wantErr:   "busy",
		},
		{
			name: "some other gio mount failure is returned as is",
			gvfs: gvfsSequence(nil),
			outputs: map[string][]result{
				"gio mount -li": {{out: gioListing}},
				mountCmd:        {{out: "gio: mtp://...: Timeout was reached\n", err: errors.New("exit status 1")}},
			},
			wantCalls: []string{"gio mount -li", mountCmd},
			wantErr:   "Timeout was reached",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeRunner{outputs: tc.outputs}
			var note bytes.Buffer
			a := Acquirer{Discover: tc.gvfs, Run: fr.run, Notice: &note}
			got, err := a.Acquire()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != phone {
					t.Errorf("mount = %+v, want %+v", got, phone)
				}
			}
			if fmt.Sprint(fr.calls) != fmt.Sprint(tc.wantCalls) {
				t.Errorf("calls = %v, want %v", fr.calls, tc.wantCalls)
			}
			if tc.wantNote != "" && !strings.Contains(note.String(), tc.wantNote) {
				t.Errorf("notice = %q, want mention of %q", note.String(), tc.wantNote)
			}
			if tc.wantNote == "" && note.Len() != 0 {
				t.Errorf("unexpected notice %q", note.String())
			}
		})
	}
}
