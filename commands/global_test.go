package commands

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli/core"
)

// globalFixture sets up a user config, a launcher folder, and a PATH that
// holds one existing command named "taken". It returns a function that runs
// "zx global ..." against the config as it is on disk at that moment.
func globalFixture(t *testing.T, userJSON string) (bin, configPath string, run func(args ...string) (string, error)) {
	t.Helper()
	home, other := t.TempDir(), t.TempDir()
	configPath = filepath.Join(home, "zxcli.json")
	bin = filepath.Join(home, "bin")
	t.Setenv("ZX_CONFIG", configPath)
	t.Setenv("ZX_BIN", "")
	t.Setenv("PATH", other)
	if err := os.WriteFile(configPath, []byte(userJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(other, "taken")
	if runtime.GOOS == "windows" {
		existing += ".cmd"
	}
	if err := os.WriteFile(existing, []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run = func(args ...string) (string, error) {
		cfg, warnings := core.LoadConfig(t.TempDir())
		if len(warnings) > 0 {
			t.Fatalf("config warnings: %v", warnings)
		}
		a, out, _ := newApp(t, "")
		if err := a.SetConfig(cfg); err != nil {
			t.Fatal(err)
		}
		err := a.Dispatch(append([]string{"global"}, args...))
		return out.String(), err
	}
	return bin, configPath, run
}

const globalConfig = `{
  "aliases": {
    "g": "guid",
    "bootstrap": { "run": "doctor --fix", "global": true },
    "taken": { "run": "guid", "global": true },
    "cd": { "run": "guid", "global": true },
  },
  "commands": [
    { "name": "gs", "summary": "git status", "run": "git status -sb", "global": true },
    { "name": "local-only", "run": "echo hi" },
  ],
}`

func TestGlobalStatusAndSync(t *testing.T) {
	bin, configPath, run := globalFixture(t, globalConfig)

	out, err := run()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"not linked  bootstrap  zx doctor --fix",
		"not linked  gs         git status -sb",
		"taken       taken      zx guid", "already a command:",
		"taken       cd         zx guid", "already a shell built-in",
		"zx global sync", "is not on your PATH yet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "local-only") || strings.Contains(out, "  g  ") {
		t.Errorf("only entries marked global belong in the list:\n%s", out)
	}
	if _, err := os.Stat(bin); err == nil {
		t.Fatal("looking at the status must not create anything")
	}

	// sync links the free names, leaves the taken ones, and exits 1 to say so.
	out, err = run("sync")
	var exit *core.ExitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Errorf("sync with a taken name should exit 1, got %v", err)
	}
	if !strings.Contains(out, "linked bootstrap") || !strings.Contains(out, "linked gs") || !strings.Contains(out, "linked      bootstrap") {
		t.Errorf("sync output:\n%s", out)
	}
	for _, name := range []string{"bootstrap", "gs"} {
		body, err := os.ReadFile(launcherPath(bin, name))
		if err != nil || !strings.Contains(string(body), "zx "+name) || !strings.Contains(string(body), globalMarker) {
			t.Errorf("launcher for %s: %v\n%s", name, err, body)
		}
	}
	for _, name := range []string{"taken", "cd", "g", "local-only"} {
		if fileExists(launcherPath(bin, name)) {
			t.Errorf("%s must not get a launcher", name)
		}
	}

	// A second sync changes nothing.
	if out, _ = run("sync"); strings.Contains(out, "linked gs") || strings.Contains(out, "removed") {
		t.Errorf("a second sync should be a no-op:\n%s", out)
	}

	// Once the folder is on PATH the hint goes away, and zx's own launchers
	// do not count as "taken".
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+bin)
	out, _ = run()
	if strings.Contains(out, "not on your PATH") || !strings.Contains(out, "linked      bootstrap") {
		t.Errorf("with the folder on PATH:\n%s", out)
	}

	// Dropping the global mark removes the launcher on the next sync.
	edited := strings.Replace(globalConfig, `"run": "git status -sb", "global": true`, `"run": "git status -sb"`, 1)
	if err := os.WriteFile(configPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ = run("sync"); !strings.Contains(out, "removed gs") || fileExists(launcherPath(bin, "gs")) {
		t.Errorf("gs should have been removed:\n%s", out)
	}
	if !fileExists(launcherPath(bin, "bootstrap")) {
		t.Error("bootstrap should still be linked")
	}

	var entries []GlobalEntry
	jsonOut, _ := run("--json")
	if err := json.Unmarshal([]byte(jsonOut), &entries); err != nil || len(entries) != 3 || entries[0].Name != "bootstrap" || entries[0].State != "linked" {
		t.Errorf("--json: %v %+v", err, entries)
	}
}

func TestGlobalNeverTouchesFilesItDidNotMake(t *testing.T) {
	bin, _, run := globalFixture(t, `{ "aliases": { "bootstrap": { "run": "doctor --fix", "global": true } } }`)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := launcherPath(bin, "bootstrap")
	stranger := filepath.Join(bin, "something-else")
	for _, f := range []string{mine, stranger} {
		if err := os.WriteFile(f, []byte("my own script\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run("sync"); err == nil || !strings.Contains(err.Error(), "was not made by zx") {
		t.Errorf("sync should refuse to overwrite a file it did not create, got %v", err)
	}
	for _, f := range []string{mine, stranger} {
		if body, _ := os.ReadFile(f); string(body) != "my own script\n" {
			t.Errorf("%s was changed", f)
		}
	}
}

func TestGlobalEmptyAndProjectRules(t *testing.T) {
	_, _, run := globalFixture(t, `{ "aliases": { "g": "guid" } }`)
	if out, err := run(); err != nil || !strings.Contains(out, "no global commands yet") || !strings.Contains(out, `"global": true`) {
		t.Errorf("empty state: %v\n%s", err, out)
	}
	if out, err := run("path"); err != nil || !strings.HasSuffix(strings.TrimSpace(out), "bin") {
		t.Errorf("global path: %v %q", err, out)
	}
	if _, err := run("nonsense"); err == nil {
		t.Error("unknown subcommands should fail")
	}

	// A project's .zx.json may define aliases and commands, but may not make
	// them machine-wide.
	project := t.TempDir()
	body := `{
  "aliases": { "ship": { "run": "check", "global": true } },
  "commands": [ { "name": "check", "run": "make check", "global": true } ]
}`
	if err := os.WriteFile(filepath.Join(project, core.ProjectFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings := core.LoadConfig(project)
	if len(warnings) != 2 || !strings.Contains(strings.Join(warnings, " "), "can't be global") {
		t.Errorf("want two warnings about global, got %v", warnings)
	}
	if len(cfg.Global) != 0 || cfg.Aliases["ship"] != "check" {
		t.Errorf("project entries still work as normal aliases, but not globally: %+v %v", cfg.Global, cfg.Aliases)
	}
	if s, ok := cfg.Script("check"); !ok || s.Global {
		t.Errorf("project command: %+v", s)
	}
}

func TestGlobalLauncherRunsZx(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a stand-in for zx")
	}
	bin, _, run := globalFixture(t, `{ "aliases": { "bootstrap": { "run": "doctor --fix", "global": true } } }`)
	if _, err := run("sync"); err != nil {
		t.Fatal(err)
	}
	// Put a stand-in "zx" on PATH that reports how it was called.
	fake := filepath.Join(os.Getenv("PATH"), "zx")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"zx was called with: $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(launcherPath(bin, "bootstrap"), "--yes", "two words").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "zx was called with: bootstrap --yes two words" {
		t.Errorf("launcher: %v %q", err, out)
	}
	if st, _ := os.Stat(launcherPath(bin, "bootstrap")); st.Mode()&0o111 == 0 {
		t.Error("the launcher is not executable")
	}
}
