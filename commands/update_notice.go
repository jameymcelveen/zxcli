package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

// How the new-version notice behaves. Tests change these.
var (
	noticeEvery = 24 * time.Hour         // how often zx looks for a newer release
	noticeWait  = 400 * time.Millisecond // the most a command is held up for the answer
	noticeTTY   = func(w io.Writer) bool { f, ok := w.(*os.File); return ok && core.IsTerminal(f) }
	noticeNow   = time.Now
)

// updateState is what zx remembers between runs, in update.json next to the
// user config.
type updateState struct {
	Checked    time.Time `json:"checked"`               // when zx last tried to look
	Latest     string    `json:"latest,omitempty"`      // the newest release it saw
	SnoozeTill time.Time `json:"snoozeUntil,omitempty"` // no notice before this
}

func updateStatePath() string {
	if d := core.UserDir(); d != "" {
		return filepath.Join(d, "update.json")
	}
	return ""
}

func loadUpdateState() updateState {
	var s updateState
	if p := updateStatePath(); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			json.Unmarshal(data, &s)
		}
	}
	return s
}

func saveUpdateState(s updateState) {
	p := updateStatePath()
	if p == "" {
		return
	}
	if data, err := json.MarshalIndent(s, "", "  "); err == nil && os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		os.WriteFile(p, data, 0o644)
	}
}

// snoozeUpdateNotice keeps zx quiet about new versions until the given time.
func snoozeUpdateNotice(until time.Time) error {
	if updateStatePath() == "" {
		return fmt.Errorf("there is nowhere to remember that on this machine")
	}
	s := loadUpdateState()
	s.SnoozeTill = until
	saveUpdateState(s)
	return nil
}

// UpdateNotice returns the hook for App.Wrap. About once a day zx asks GitHub
// for the latest release, in the background so the command is not slowed, and
// when a newer one exists it says so on stderr after the command's own output.
// It stays quiet in scripts and pipes, in development builds, for --json, and
// while snoozed. Turn it off with the setting update.check = off or the
// environment variable ZX_NO_UPDATE_CHECK.
func UpdateNotice(app *core.App) func(args []string) func() {
	return func(args []string) func() {
		if !noticeWanted(app, args) {
			return nil
		}
		state := loadUpdateState()
		now := noticeNow()
		var result chan string
		if now.Sub(state.Checked) >= noticeEvery {
			// Record the attempt first, so being offline costs at most one
			// short wait a day.
			state.Checked = now
			saveUpdateState(state)
			result = make(chan string, 1)
			go func() {
				latest, err := latestRelease(&http.Client{Timeout: 3 * time.Second})
				if err != nil {
					result <- ""
					return
				}
				s := loadUpdateState()
				s.Checked, s.Latest = now, latest
				saveUpdateState(s)
				result <- latest
			}()
		}
		return func() {
			latest := state.Latest
			if result != nil {
				select {
				case l := <-result:
					if l != "" {
						latest = l
					}
				case <-time.After(noticeWait):
				}
			}
			cmp, known := CompareVersions(core.Version, latest)
			if !known || cmp >= 0 || noticeNow().Before(state.SnoozeTill) {
				return
			}
			fmt.Fprintf(app.Stderr, "\n%s\n%s\n",
				app.Bold(fmt.Sprintf("zx %s is available (you have %s). Run: zx upgrade", latest, core.Version)),
				app.Dim("Not now? Stay quiet for a week: zx upgrade --snooze"))
		}
	}
}

// noticeWanted is false whenever a message from zx would be unwelcome.
func noticeWanted(app *core.App, args []string) bool {
	if _, known := CompareVersions(core.Version, core.Version); !known { // a development build
		return false
	}
	if os.Getenv("ZX_NO_UPDATE_CHECK") != "" || !noticeTTY(app.Stderr) {
		return false
	}
	switch strings.ToLower(app.Config.Setting("update.check", "on")) {
	case "off", "false", "no", "never", "0":
		return false
	}
	if len(args) > 0 {
		switch args[0] {
		case "upgrade", "completion", "__complete", "help", "-h", "--help":
			return false
		}
	}
	for _, a := range args {
		if a == "--json" {
			return false
		}
	}
	return true
}
