package editor

import (
	"bytes"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.bytestone.uk/hum3/photo-importer/internal/deletions"
)

func writeJPEG(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, landscape(), nil)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	root, cache string
	srv         *httptest.Server
	q           *deletions.Queue
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	// A sibling secret outside root that must never be reachable.
	writeFile(t, filepath.Dir(root), "secret.txt", "s3cret")
	writeJPEG(t, root, "Camera/20260101_a.jpg")
	writeJPEG(t, root, "Camera/20260102_b.jpg")
	writeFile(t, root, "Camera/20260103_c.mp4", "video")
	writeJPEG(t, root, "Screenshots/s.png")
	q, err := deletions.Load()
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	ed, err := New(root, q, cache)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(ed.Handler())
	t.Cleanup(srv.Close)
	return &fixture{root: root, cache: cache, srv: srv, q: q}
}

func (f *fixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(f.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// post submits a form without following the redirect.
func (f *fixture) post(t *testing.T, path string, form url.Values) *http.Response {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(f.srv.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	return resp
}

func TestRootListsSubdirectoriesAndNoFiles(t *testing.T) {
	f := newFixture(t)
	code, body := f.get(t, "/")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"Camera", "Screenshots"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	if strings.Contains(body, "20260101_a.jpg") {
		t.Error("root must not list files of subdirectories")
	}
}

func TestDirectoryListsFilesNewestFirstWithThumbsAndCheckboxes(t *testing.T) {
	f := newFixture(t)
	_, body := f.get(t, "/?dir=Camera")
	a, b, c := strings.Index(body, "20260101_a.jpg"), strings.Index(body, "20260102_b.jpg"), strings.Index(body, "20260103_c.mp4")
	if a < 0 || b < 0 || c < 0 {
		t.Fatalf("files missing in\n%s", body)
	}
	if c >= b || b >= a {
		t.Errorf("expected newest first, got positions c=%d b=%d a=%d", c, b, a)
	}
	if !strings.Contains(body, `name="p" value="Camera/20260101_a.jpg"`) {
		t.Error("expected a checkbox per file")
	}
	if !strings.Contains(body, "/thumb?p=Camera%2F20260101_a.jpg") {
		t.Error("expected a thumbnail img per image")
	}
	if strings.Contains(body, "/thumb?p=Camera%2F20260103_c.mp4") {
		t.Error("videos get no thumbnail request")
	}
}

func TestPagination(t *testing.T) {
	f := newFixture(t)
	for i := range 5 {
		writeFile(t, f.root, "Many/"+string(rune('a'+i))+".txt", "x")
	}
	// Re-create the editor with a small page size.
	ed, _ := New(f.root, f.q, f.cache)
	ed.PageSize = 2
	srv := httptest.NewServer(ed.Handler())
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/?dir=Many&page=2")
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, "c.txt") || !strings.Contains(body, "b.txt") || strings.Contains(body, "a.txt") {
		t.Fatalf("page 2 of size 2, newest first, should hold c and b:\n%s", body)
	}
	if !strings.Contains(body, "page 2 of 3") {
		t.Errorf("expected page indicator in\n%s", body)
	}
}

func TestThumbIsServedAndCached(t *testing.T) {
	f := newFixture(t)
	resp, err := http.Get(f.srv.URL + "/thumb?p=Camera%2F20260101_a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if _, err := jpeg.Decode(resp.Body); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(f.cache)
	if len(entries) != 1 {
		t.Fatalf("expected one cached thumbnail, got %d", len(entries))
	}
	code, _ := f.get(t, "/thumb?p=Camera%2F20260101_a.jpg")
	if code != 200 {
		t.Fatal(code)
	}
	entries, _ = os.ReadDir(f.cache)
	if len(entries) != 1 {
		t.Fatalf("second request must hit the cache, got %d entries", len(entries))
	}
}

func TestFullSizeFileServed(t *testing.T) {
	f := newFixture(t)
	code, body := f.get(t, "/file?p=Camera%2F20260103_c.mp4")
	if code != 200 || body != "video" {
		t.Fatalf("status %d body %q", code, body)
	}
}

func TestPathsCannotEscapeRoot(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/file?p=..%2Fsecret.txt", "/thumb?p=..%2Fsecret.txt", "/file?p=%2F..%2Fsecret.txt"} {
		code, body := f.get(t, p)
		if code == 200 || strings.Contains(body, "s3cret") {
			t.Errorf("%s: status %d body %q", p, code, body)
		}
	}
	code, body := f.get(t, "/?dir=..")
	if code != 200 || strings.Contains(body, "secret.txt") {
		t.Errorf("dir=..: status %d listed secret", code)
	}
}

func TestDeleteRemovesLocallyQueuesAndRedirects(t *testing.T) {
	f := newFixture(t)
	resp := f.post(t, "/delete", url.Values{
		"p":    {"Camera/20260101_a.jpg", "Camera/20260103_c.mp4"},
		"dir":  {"Camera"},
		"page": {"1"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/?dir=Camera" {
		t.Fatalf("location %q", loc)
	}
	for _, rel := range []string{"Camera/20260101_a.jpg", "Camera/20260103_c.mp4"} {
		if _, err := os.Stat(filepath.Join(f.root, rel)); !os.IsNotExist(err) {
			t.Errorf("%s should be deleted locally", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "Camera/20260102_b.jpg")); err != nil {
		t.Error("unselected file must survive")
	}
	q, _ := deletions.Load()
	got := q.Paths()
	if len(got) != 2 || got[0] != "Camera/20260101_a.jpg" || got[1] != "Camera/20260103_c.mp4" {
		t.Fatalf("queue %v", got)
	}
	_, body := f.get(t, "/?dir=Camera&page=1")
	if !strings.Contains(body, "Deleted 2 file(s)") || !strings.Contains(body, "20260101_a.jpg") {
		t.Errorf("expected flash naming the deletions in\n%s", body)
	}
	if strings.Contains(body, `value="Camera/20260101_a.jpg"`) {
		t.Error("deleted file must no longer be listed")
	}
	_, body = f.get(t, "/?dir=Camera")
	if strings.Contains(body, "Deleted 2 file(s)") {
		t.Error("flash must show once")
	}
}

func TestDeleteNothingSelectedIsHarmless(t *testing.T) {
	f := newFixture(t)
	resp := f.post(t, "/delete", url.Values{"dir": {"Camera"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d", resp.StatusCode)
	}
	q, _ := deletions.Load()
	if q.Len() != 0 {
		t.Fatal("nothing should be queued")
	}
}

func TestDeleteOutsideRootRefused(t *testing.T) {
	f := newFixture(t)
	f.post(t, "/delete", url.Values{"p": {"../secret.txt"}})
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.root), "secret.txt")); err != nil {
		t.Fatal("secret must survive")
	}
	q, _ := deletions.Load()
	if q.Len() != 0 {
		t.Fatalf("queue %v", q.Paths())
	}
}

func TestQueuePageListsPendingDeletions(t *testing.T) {
	f := newFixture(t)
	_ = f.q.Add("Camera/old.jpg")
	code, body := f.get(t, "/queue")
	if code != 200 || !strings.Contains(body, "Camera/old.jpg") || !strings.Contains(body, "1 file(s)") {
		t.Fatalf("status %d body\n%s", code, body)
	}
	_, body = f.get(t, "/")
	if !strings.Contains(body, "Deletion queue (1)") {
		t.Errorf("navbar should show queue count in\n%s", body)
	}
}
