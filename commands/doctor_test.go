package commands

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli/core"
)

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		have, want string
		ok         bool
	}{
		{"22.3.1", "22", true},
		{"22.3.1", "22.3", true},
		{"22.3.1", "22.4", false},
		{"10.0.401", "10.0", true},
		{"9.0.100", "10.0", false},
		{"1.27.2", "1.22", true},
		{"1.9", "1.22", false},
		{"3.81", "4", false},
		{"2.50.1", "2.50.1", true},
		{"2.50", "2.50.1", false},
		{"v20.11.0", "v20", true},
	}
	for _, tc := range cases {
		if got := VersionAtLeast(tc.have, tc.want); got != tc.ok {
			t.Errorf("VersionAtLeast(%s, %s) = %v, want %v", tc.have, tc.want, got, tc.ok)
		}
	}
}

func TestBuiltinFixUsesThePackageManagerThatExists(t *testing.T) {
	old := doctorLookPath
	defer func() { doctorLookPath = old }()

	doctorLookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	cases := []struct{ tool, goos, install, upgrade string }{
		{"node", "darwin", "brew install node", "brew upgrade node"},
		{"dotnet", "darwin", "brew install --cask dotnet-sdk", "brew upgrade --cask dotnet-sdk"},
		{"make", "darwin", "xcode-select --install", ""},
		{"gh", "windows", "winget install -e --id GitHub.cli", "winget upgrade -e --id GitHub.cli"},
		{"git", "linux", "sudo apt-get install -y git", "sudo apt-get install -y git"},
		{"terraform", "darwin", "", ""},
		{"git", "plan9", "", ""},
	}
	for _, tc := range cases {
		if install, upgrade := BuiltinFix(tc.tool, tc.goos); install != tc.install || upgrade != tc.upgrade {
			t.Errorf("BuiltinFix(%s, %s) = %q, %q", tc.tool, tc.goos, install, upgrade)
		}
	}
	// With no package manager on the machine, zx suggests nothing.
	doctorLookPath = func(string) (string, error) { return "", errors.New("not found") }
	for _, goos := range []string{"darwin", "windows", "linux"} {
		if install, _ := BuiltinFix("git", goos); install != "" {
			t.Errorf("no package manager on %s, but got %q", goos, install)
		}
	}
}

// doctorApp returns an app whose user config lives in a temp folder, with the
// given machine and project checks loaded from real config files.
func doctorApp(t *testing.T, userJSON, projectJSON string) func(args ...string) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ZX_CONFIG", filepath.Join(home, "zxcli.json"))
	if userJSON != "" {
		if err := os.WriteFile(filepath.Join(home, "zxcli.json"), []byte(userJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	project := t.TempDir()
	if projectJSON != "" {
		if err := os.WriteFile(filepath.Join(project, core.ProjectFile), []byte(projectJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return func(args ...string) (string, string, error) {
		cfg, warnings := core.LoadConfig(project)
		if len(warnings) > 0 {
			t.Fatalf("config warnings: %v", warnings)
		}
		a, out, errOut := newApp(t, "")
		if err := a.SetConfig(cfg); err != nil {
			t.Fatal(err)
		}
		err := a.Dispatch(append([]string{"doctor"}, args...))
		return out.String(), errOut.String(), err
	}
}

func TestDoctorVersionRequirements(t *testing.T) {
	run := doctorApp(t, `{
  "doctor": [
    "go",
    { "tool": "zx-no-such-tool", "min": "3.2", "install": "echo install-it" },
  ]
}`, `{
  "doctor": [
    { "tool": "go", "min": "999.0", "upgrade": { "mac": "echo up-mac", "linux": "echo up-linux", "windows": "echo up-win" } },
  ]
}`)
	out, _, err := run()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"This machine", "This project",
		"missing  zx-no-such-tool  not installed (needs 3.2+)",
		"too old  go", "(needs 999.0+)",
		"2 of 2 checks need attention", "Fixes", "echo install-it", "zx doctor --fix",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "echo up-mac") && !strings.Contains(out, "echo up-linux") && !strings.Contains(out, "echo up-win") {
		t.Errorf("the per-OS upgrade command is missing:\n%s", out)
	}
	// The project's entry for go replaces the machine's, so go is listed once.
	if n := strings.Count(out, "  go "); n != 1 {
		t.Errorf("go should be listed once, in the project section; found %d:\n%s", n, out)
	}

	jsonOut, _, err := run("--json")
	if err != nil {
		t.Fatal(err)
	}
	var checks []Check
	if err := json.Unmarshal([]byte(jsonOut), &checks); err != nil {
		t.Fatal(err)
	}
	last := checks[len(checks)-1]
	if last.Name != "go" || last.Status != "too old" || last.Source != "project" || last.OK || !strings.HasPrefix(last.Fix, "echo up-") {
		t.Errorf("--json last check = %+v", last)
	}
}

func TestDoctorProjectChecksNeedApproval(t *testing.T) {
	pass, fail := "exit 0", "exit 3"
	run := doctorApp(t, `{ "doctor": [] }`, `{
  "doctor": [
    { "name": "passes", "run": "`+pass+`" },
    { "name": "fails", "run": "`+fail+`" },
  ]
}`)
	// Before approval nothing from the project runs.
	out, _, err := run("--strict")
	var exit *core.ExitError
	if !errors.As(err, &exit) {
		t.Errorf("unapproved checks need attention, so --strict should exit 1; got %v", err)
	}
	if strings.Count(out, "skipped") != 2 || !strings.Contains(out, "zx doctor --trust") {
		t.Errorf("before --trust:\n%s", out)
	}

	// --trust shows the commands, records the approval, and runs them.
	out, errOut, _ := run("--trust")
	if !strings.Contains(errOut, "approved 2 custom checks") || !strings.Contains(errOut, pass) || !strings.Contains(errOut, fail) {
		t.Errorf("--trust should list what it approved:\n%s", errOut)
	}
	if !strings.Contains(out, "ok       passes") || !strings.Contains(out, "failed   fails") || !strings.Contains(out, "1 of 2 checks need attention") {
		t.Errorf("after --trust:\n%s", out)
	}

	// The approval is remembered.
	if out, _, _ = run(); strings.Contains(out, "skipped") {
		t.Errorf("approval was not remembered:\n%s", out)
	}
}

func TestDoctorApprovalIsVoidWhenChecksChange(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("ZX_CONFIG", filepath.Join(home, "zxcli.json"))
	file := filepath.Join(project, core.ProjectFile)
	doctor := func(body string, args ...string) string {
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, _ := core.LoadConfig(project)
		a, out, _ := newApp(t, "")
		if err := a.SetConfig(cfg); err != nil {
			t.Fatal(err)
		}
		a.Dispatch(append([]string{"doctor"}, args...))
		return out.String()
	}
	original := `{ "doctor": [ { "name": "probe", "run": "exit 0" } ] }`
	if out := doctor(original, "--trust"); !strings.Contains(out, "ok       probe") {
		t.Fatalf("after --trust:\n%s", out)
	}
	changed := `{ "doctor": [ { "name": "probe", "run": "exit 0 && echo something-new" } ] }`
	if out := doctor(changed); !strings.Contains(out, "skipped  probe") {
		t.Errorf("an edited check must be approved again:\n%s", out)
	}
	// The approval on file is still for the original text, so going back to
	// it needs no new approval.
	if out := doctor(original); !strings.Contains(out, "ok       probe") {
		t.Errorf("the original check should still be approved:\n%s", out)
	}
}

func TestDoctorMinimumVersionMet(t *testing.T) {
	run := doctorApp(t, `{ "doctor": [ { "tool": "go", "min": "1.0" } ] }`, "")
	out, _, err := run("--strict")
	if err != nil || !strings.Contains(out, "ok       go") || !strings.Contains(out, "everything checks out") {
		t.Errorf("err=%v\n%s", err, out)
	}
	if strings.Contains(out, "This project") {
		t.Errorf("no project config, so no project section:\n%s", out)
	}
}

func TestDoctorFix(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "installed.txt")
	probe, install := `test -f "`+marker+`"`, `echo done > "`+marker+`"`
	if runtime.GOOS == "windows" {
		probe = `if not exist "` + marker + `" exit 1`
	}
	// Backslashes in Windows paths have to be escaped inside the JSON config.
	esc := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) }
	userJSON := `{ "doctor": [ { "name": "marker file", "run": "` + esc(probe) + `", "install": "` + esc(install) + `" } ] }`

	setup := func(stdin string) (*core.App, func() (string, string)) {
		home := t.TempDir()
		t.Setenv("ZX_CONFIG", filepath.Join(home, "zxcli.json"))
		if err := os.WriteFile(filepath.Join(home, "zxcli.json"), []byte(userJSON), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, warnings := core.LoadConfig(t.TempDir())
		if len(warnings) > 0 {
			t.Fatalf("config warnings: %v", warnings)
		}
		a, out, errOut := newApp(t, stdin)
		if err := a.SetConfig(cfg); err != nil {
			t.Fatal(err)
		}
		return a, func() (string, string) { return out.String(), errOut.String() }
	}

	// Checks in your own config run without approval, and report the fix.
	a, output := setup("")
	if err := a.Dispatch([]string{"doctor"}); err != nil {
		t.Fatal(err)
	}
	if out, _ := output(); !strings.Contains(out, "failed   marker file") || !strings.Contains(out, "Fixes") {
		t.Fatalf("before the fix:\n%s", out)
	}

	// Answering anything but yes runs nothing.
	for _, answer := range []string{"n\n", "\n", ""} {
		a, output = setup(answer)
		if err := a.Dispatch([]string{"doctor", "--fix"}); err != nil {
			t.Fatal(err)
		}
		if _, errOut := output(); !strings.Contains(errOut, "will run:") || !strings.Contains(errOut, "nothing was run") {
			t.Errorf("answer %q:\n%s", answer, errOut)
		}
		if fileExists(marker) {
			t.Fatalf("answer %q still ran the install", answer)
		}
	}

	// Yes runs the install, and the report afterwards shows the check passing.
	a, output = setup("y\n")
	if err := a.Dispatch([]string{"doctor", "--fix", "--strict"}); err != nil {
		t.Fatalf("after a successful fix --strict should pass: %v", err)
	}
	out, errOut := output()
	if !fileExists(marker) || !strings.Contains(errOut, "running") || !strings.Contains(out, "ok       marker file") || !strings.Contains(out, "everything checks out") {
		t.Errorf("after the fix:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}

	// --yes skips the question.
	os.Remove(marker)
	a, _ = setup("")
	if err := a.Dispatch([]string{"doctor", "--fix", "--yes"}); err != nil || !fileExists(marker) {
		t.Errorf("--fix --yes did not run the install: %v", err)
	}

	// With nothing broken, --fix has nothing to do.
	a, output = setup("")
	if err := a.Dispatch([]string{"doctor", "--fix"}); err != nil {
		t.Fatal(err)
	}
	if _, errOut := output(); !strings.Contains(errOut, "nothing to fix") {
		t.Errorf("nothing broken:\n%s", errOut)
	}
}

func TestDoctorConfigForms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZX_CONFIG", filepath.Join(home, "zxcli.json"))
	body := `{
  "doctor": [
    "git",
    { "tool": "node", "min": "22", "install": { "mac": "m", "windows": "w", "linux": "l" } },
    { "name": "db", "run": "pg_isready", "install": "start-db" },
    { "name": "no run line" },
    { "tool": "both", "run": "x" },
    { "tool": "not a program" },
  ]
}`
	if err := os.WriteFile(filepath.Join(home, "zxcli.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings := core.LoadConfig(t.TempDir())
	if len(warnings) != 3 {
		t.Errorf("want 3 warnings for the 3 bad entries, got %v", warnings)
	}
	if len(cfg.UserDoctor) != 3 {
		t.Fatalf("UserDoctor = %+v", cfg.UserDoctor)
	}
	node, db := cfg.UserDoctor[1], cfg.UserDoctor[2]
	if cfg.UserDoctor[0].Tool != "git" || node.Min != "22" || node.Install.For("darwin") != "m" || node.Install.For("windows") != "w" || node.Install.For("linux") != "l" {
		t.Errorf("parsed checks = %+v", cfg.UserDoctor)
	}
	if db.Label() != "db" || db.Install.For("linux") != "start-db" || db.Install.For("darwin") != "start-db" {
		t.Errorf("a plain install string should apply to every OS: %+v", db)
	}
	// No "doctor" list at all falls back to the doctor.tools setting.
	plan := DoctorPlan(core.Config{Settings: map[string]string{"doctor.tools": "a, b"}})
	if len(plan) != 2 || plan[0].Tool != "a" || plan[1].Tool != "b" {
		t.Errorf("doctor.tools fallback = %+v", plan)
	}
	if plan := DoctorPlan(core.Config{}); len(plan) != len(strings.Split(DefaultDoctorTools, ",")) {
		t.Errorf("default plan has %d entries", len(plan))
	}
}
