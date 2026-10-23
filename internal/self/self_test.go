package self

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLatestVersionParsesProxyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+moduleName+"/@latest" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Version":"v0.3.1","Time":"2026-10-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	got, err := latestVersion(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.3.1" {
		t.Errorf("latestVersion = %q, want v0.3.1", got)
	}
}

func TestLatestVersionNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := latestVersion(srv.URL); err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestUpToDateIgnoresVPrefix(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", true},
		{"0.1.0", "v0.2.0", false},
	}
	for _, c := range cases {
		if got := upToDate(c.current, c.latest); got != c.want {
			t.Errorf("upToDate(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestRunRejectsUnknownSubcommand(t *testing.T) {
	err := Run([]string{"bogus"}, "0.1.0")
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v, want unknown self command", err)
	}
	if err := Run(nil, "0.1.0"); err == nil {
		t.Fatal("expected usage error with no args")
	}
}
