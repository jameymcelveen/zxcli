package commands

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

type noticeFixture struct {
	hits   *atomic.Int32
	errOut *bytes.Buffer
	app    *core.App
	run    func(args ...string) string // runs the hook around a pretend command and returns stderr
	now    time.Time
}

func newNoticeFixture(t *testing.T, version, latest string) *noticeFixture {
	t.Helper()
	t.Setenv("ZX_CONFIG", filepath.Join(t.TempDir(), "zxcli.json"))
	t.Setenv("ZX_NO_UPDATE_CHECK", "")
	hits := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if latest == "" {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, `{"tag_name":"`+latest+`"}`)
	}))
	oldAPI, oldVersion, oldTTY, oldNow, oldWait := upgradeAPI, core.Version, noticeTTY, noticeNow, noticeWait
	f := &noticeFixture{hits: hits, now: time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)}
	upgradeAPI, core.Version = srv.URL, version
	noticeTTY = func(io.Writer) bool { return true }
	noticeNow = func() time.Time { return f.now }
	noticeWait = 3 * time.Second
	t.Cleanup(func() {
		srv.Close()
		upgradeAPI, core.Version, noticeTTY, noticeNow, noticeWait = oldAPI, oldVersion, oldTTY, oldNow, oldWait
	})
	app, _, errOut := newApp(t, "")
	f.app, f.errOut = app, errOut
	f.run = func(args ...string) string {
		errOut.Reset()
		if done := UpdateNotice(app)(args); done != nil {
			done()
		}
		return errOut.String()
	}
	return f
}

func TestUpdateNoticeAppearsOnceReleaseIsNewer(t *testing.T) {
	f := newNoticeFixture(t, "v0.6.1", "v0.7.0")
	out := f.run("doctor")
	if !strings.Contains(out, "zx v0.7.0 is available (you have v0.6.1). Run: zx upgrade") || !strings.Contains(out, "zx upgrade --snooze") {
		t.Fatalf("notice = %q", out)
	}
	if f.hits.Load() != 1 {
		t.Errorf("expected one lookup, got %d", f.hits.Load())
	}
	// Within a day the answer comes from the saved state: no second lookup.
	f.now = f.now.Add(2 * time.Hour)
	if out := f.run("guid"); !strings.Contains(out, "v0.7.0 is available") || f.hits.Load() != 1 {
		t.Errorf("cached notice = %q, lookups = %d", out, f.hits.Load())
	}
	// After a day it looks again.
	f.now = f.now.Add(25 * time.Hour)
	f.run("guid")
	if f.hits.Load() != 2 {
		t.Errorf("expected a second lookup after a day, got %d", f.hits.Load())
	}
}

func TestUpdateNoticeSnooze(t *testing.T) {
	f := newNoticeFixture(t, "v0.6.1", "v0.7.0")
	f.run("doctor")
	// zx upgrade --snooze --days 3
	app, out, _ := newApp(t, "")
	if err := app.Dispatch([]string{"upgrade", "--snooze", "--days", "3"}); err != nil || !strings.Contains(out.String(), "3 days") {
		t.Fatalf("snooze: %v %q", err, out.String())
	}
	f.now = time.Now().Add(24 * time.Hour)
	noticeNow = func() time.Time { return f.now }
	if out := f.run("doctor"); out != "" {
		t.Errorf("snoozed, but got %q", out)
	}
	f.now = time.Now().AddDate(0, 0, 4)
	if out := f.run("doctor"); !strings.Contains(out, "v0.7.0 is available") {
		t.Errorf("snooze should end after 3 days, got %q", out)
	}
	if err := newAppDispatch(t, "upgrade", "--snooze", "--days", "0"); err == nil {
		t.Error("--days 0 should be refused")
	}
}

func newAppDispatch(t *testing.T, args ...string) error {
	t.Helper()
	app, _, _ := newApp(t, "")
	return app.Dispatch(args)
}

func TestUpdateNoticeStaysQuiet(t *testing.T) {
	cases := []struct {
		name    string
		version string
		latest  string
		setup   func(f *noticeFixture, t *testing.T)
		args    []string
	}{
		{"up to date", "v0.7.0", "v0.7.0", nil, []string{"doctor"}},
		{"ahead of the release", "v0.8.0", "v0.7.0", nil, []string{"doctor"}},
		{"development build", "dev", "v0.7.0", nil, []string{"doctor"}},
		{"not a terminal", "v0.6.1", "v0.7.0", func(f *noticeFixture, t *testing.T) { noticeTTY = func(io.Writer) bool { return false } }, []string{"doctor"}},
		{"json output", "v0.6.1", "v0.7.0", nil, []string{"doctor", "--json"}},
		{"upgrade itself", "v0.6.1", "v0.7.0", nil, []string{"upgrade"}},
		{"turned off by setting", "v0.6.1", "v0.7.0", func(f *noticeFixture, t *testing.T) {
			f.app.SetConfig(core.Config{Settings: map[string]string{"update.check": "off"}})
		}, []string{"doctor"}},
		{"turned off by environment", "v0.6.1", "v0.7.0", func(f *noticeFixture, t *testing.T) { t.Setenv("ZX_NO_UPDATE_CHECK", "1") }, []string{"doctor"}},
		{"release lookup fails", "v0.6.1", "", nil, []string{"doctor"}},
	}
	for _, tc := range cases {
		f := newNoticeFixture(t, tc.version, tc.latest)
		if tc.setup != nil {
			tc.setup(f, t)
		}
		if out := f.run(tc.args...); out != "" {
			t.Errorf("%s: expected silence, got %q", tc.name, out)
		}
	}
}

func TestFailureLinePrefersTheErrorOverTheBanner(t *testing.T) {
	docker := "Client:\n Version:    29.8.2\n\nServer:\nERROR: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\nerrors pretty printing info\n"
	if got := failureLine(docker); !strings.HasPrefix(got, "Cannot connect to the Docker daemon") {
		t.Errorf("got %q", got)
	}
	if got := failureLine("first\nsecond\n"); got != "first" {
		t.Errorf("with no error-looking line the first line should win, got %q", got)
	}
	if got := failureLine("  \n"); got != "" {
		t.Errorf("got %q", got)
	}
}
