package commands

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli"
	"github.com/jameymcelveen/zxcli/core"
)

// The manifest and the Go code have to agree: every registered command needs
// a zxcli.json entry, and every entry needs code behind it.
func TestManifestMatchesCode(t *testing.T) {
	var m core.Manifest
	if err := json.Unmarshal(zxcli.Manifest, &m); err != nil {
		t.Fatalf("zxcli.json: %v", err)
	}
	registered := map[string]bool{}
	for _, n := range core.Registered() {
		registered[n] = true
	}
	listed := map[string]bool{}
	taken := map[string]string{}
	for _, cmd := range m.Commands {
		listed[cmd.Name] = true
		if !registered[cmd.Name] {
			t.Errorf("zxcli.json lists %q but no Go file registers it", cmd.Name)
		}
		if strings.TrimSpace(cmd.Summary) == "" {
			t.Errorf("%q has no summary in zxcli.json", cmd.Name)
		}
		for _, n := range append([]string{cmd.Name}, cmd.Aliases...) {
			if other, dup := taken[n]; dup {
				t.Errorf("name %q is used by both %q and %q", n, other, cmd.Name)
			}
			taken[n] = cmd.Name
		}
		if cmd.Hidden {
			continue
		}
		file := strings.ReplaceAll(cmd.Name, "-", "_") + ".go"
		if _, err := os.Stat(file); err != nil {
			t.Errorf("command %q should live in commands/%s", cmd.Name, file)
		}
		if cmd.Group == "" {
			t.Errorf("%q has no group in zxcli.json", cmd.Name)
		}
	}
	for n := range registered {
		if !listed[n] {
			t.Errorf("%q is registered in Go but missing from zxcli.json", n)
		}
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	app, _, _ := newApp(t, "")
	for _, cmd := range app.Commands() {
		out, _, err := run(t, "", "help", cmd.Name)
		if err != nil {
			t.Errorf("zx help %s: %v", cmd.Name, err)
		}
		if !strings.Contains(out, "zx "+cmd.Name) || !strings.Contains(out, "Usage") {
			t.Errorf("zx help %s printed unexpected output:\n%s", cmd.Name, out)
		}
	}
}

func TestTopLevelHelpListsCommands(t *testing.T) {
	out := mustRun(t, "")
	for _, want := range []string{"guid", "Generate", "zx help <command>", "--copy"} {
		if !strings.Contains(out, want) {
			t.Errorf("help is missing %q", want)
		}
	}
	if strings.Contains(out, "__complete") {
		t.Error("hidden commands should not be listed")
	}
}

func TestUnknownCommandSuggests(t *testing.T) {
	_, _, err := run(t, "", "giud")
	if err == nil || !strings.Contains(err.Error(), `"guid"`) {
		t.Errorf("expected a suggestion for guid, got %v", err)
	}
}

func TestVersionAndCompletion(t *testing.T) {
	if out := mustRun(t, "", "version"); !strings.HasPrefix(out, "zx ") {
		t.Errorf("version printed %q", out)
	}
	if out := mustRun(t, "", "--version"); !strings.HasPrefix(out, "zx ") {
		t.Errorf("--version printed %q", out)
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		if out := mustRun(t, "", "completion", shell); !strings.Contains(out, "__complete") {
			t.Errorf("%s completion does not call __complete", shell)
		}
	}
	names := strings.Fields(mustRun(t, "", "__complete"))
	if len(names) < 10 || !strings.Contains(" "+strings.Join(names, " ")+" ", " guid ") {
		t.Errorf("__complete printed %v", names)
	}
	if _, _, err := run(t, "", "completion", "tcsh"); err == nil {
		t.Error("expected an error for an unsupported shell")
	}
}
