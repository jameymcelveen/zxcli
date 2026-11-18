package commands

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli"
	"github.com/jameymcelveen/zxcli/core"
)

func TestConfigInitWritesAUsableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zx", "zxcli.json")
	t.Setenv("ZX_CONFIG", path)

	if out := mustRun(t, "", "config"); !strings.Contains(out, "not created yet") {
		t.Errorf("config before init:\n%s", out)
	}
	if out := mustRun(t, "", "config", "init"); !strings.Contains(out, "created") {
		t.Errorf("config init printed %q", out)
	}
	if out := mustRun(t, "", "config", "init"); !strings.Contains(out, "left alone") {
		t.Errorf("a second init should not overwrite: %q", out)
	}
	if got := mustRun(t, "", "config", "path"); got != path {
		t.Errorf("config path = %q, want %q", got, path)
	}

	// The starter file must load cleanly and define what its comments promise.
	cfg, warnings := core.LoadConfig(t.TempDir())
	if len(warnings) > 0 {
		t.Fatalf("starter config has problems: %v", warnings)
	}
	if cfg.Aliases["g"] != "guid" {
		t.Errorf("starter aliases = %v", cfg.Aliases)
	}
	if s, ok := cfg.Script("gs"); !ok || s.Run != "git status -sb" {
		t.Errorf("starter script gs = %+v", s)
	}

	// With the config applied, the alias runs the real command.
	app, out, _ := newApp(t, "")
	if err := app.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.Dispatch([]string{"g"}); err != nil {
		t.Fatal(err)
	}
	if !uuidV4.MatchString(strings.TrimSpace(out.String())) {
		t.Errorf("alias g printed %q", out.String())
	}
}

func TestDisabledCommandsDisappear(t *testing.T) {
	app, out, _ := newApp(t, "")
	if err := app.SetConfig(core.Config{Disabled: map[string]bool{"lorem": true}}); err != nil {
		t.Fatal(err)
	}
	if err := app.Dispatch([]string{"lorem"}); err == nil {
		t.Error("a disabled command should not run")
	}
	if err := app.Dispatch(nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "lorem") {
		t.Error("a disabled command should not be listed in help")
	}
}

// fakeRepo builds a minimal copy of the source tree for the scaffolder.
func fakeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"go.mod":     []byte("module example.com/fork/zxcli\n\ngo 1.22\n"),
		"zxcli.json": zxcli.Manifest,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestScaffoldAddsACommand(t *testing.T) {
	root := fakeRepo(t)
	// Start from a subfolder to prove the repo root is found by walking up.
	files, err := Scaffold(filepath.Join(root, "commands"), "zz-sample", "Roll some dice", "Generate")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("Scaffold returned %v", files)
	}

	src, err := os.ReadFile(filepath.Join(root, "commands", "zz_sample.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"example.com/fork/zxcli/core"`, `core.Register("zz-sample", runZzSample)`} {
		if !strings.Contains(string(src), want) {
			t.Errorf("generated command is missing %s", want)
		}
	}
	for _, name := range []string{"zz_sample.go", "zz_sample_test.go"} {
		if _, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "commands", name), nil, parser.AllErrors); err != nil {
			t.Errorf("generated %s is not valid Go: %v", name, err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(root, "zxcli.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m core.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest is no longer valid JSON: %v", err)
	}
	var found *core.Meta
	for i := range m.Commands {
		if m.Commands[i].Name == "zz-sample" {
			found = &m.Commands[i]
		}
	}
	if found == nil || found.Summary != "Roll some dice" || found.Group != "Generate" {
		t.Fatalf("manifest entry = %+v", found)
	}
	if last := m.Commands[len(m.Commands)-1]; !last.Hidden {
		t.Errorf("hidden commands should stay last, but the last one is %q", last.Name)
	}
	if strings.Contains(string(raw), "u003c") || !strings.Contains(string(raw), "zx jwt <token>") {
		t.Error("manifest rewrite escaped < and >")
	}
}

func TestScaffoldRefusesBadInput(t *testing.T) {
	root := fakeRepo(t)
	for _, name := range []string{"guid", "uuid", "Bad Name", "9lives", ""} {
		if _, err := Scaffold(root, name, "", "Commands"); err == nil {
			t.Errorf("Scaffold(%q) should fail", name)
		}
	}
	if _, err := Scaffold(t.TempDir(), "zz-other", "", "Commands"); err == nil {
		t.Error("Scaffold outside a zxcli checkout should fail")
	}
	if _, err := Scaffold(root, "zz-other", "", "Commands"); err != nil {
		t.Fatal(err)
	}
	if _, err := Scaffold(root, "zz-other", "", "Commands"); err == nil {
		t.Error("scaffolding the same name twice should fail")
	}
}
