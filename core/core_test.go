package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const testManifest = `{
  "tagline": "test build",
  "commands": [
    {"name": "echo", "summary": "Print the arguments", "group": "Test", "aliases": ["say"], "examples": ["zx echo hi"]},
    {"name": "fail", "summary": "Always fails", "group": "Test"},
    {"name": "help", "summary": "Show help", "group": "Test"},
    {"name": "secret", "summary": "Hidden", "hidden": true},
    {"name": "ghost", "summary": "Listed but never registered"}
  ]
}`

func init() {
	Register("echo", func(c *Context) error {
		fs := c.Flags()
		upper := fs.Bool("u", false, "uppercase")
		times := fs.Int("times", 1, "repeat `count` times")
		pos, err := c.Parse(fs)
		if err != nil {
			return err
		}
		line := strings.Join(pos, " ")
		if *upper {
			line = strings.ToUpper(line)
		}
		for i := 0; i < *times; i++ {
			c.Println(line)
		}
		return nil
	})
	Register("fail", func(c *Context) error { return errors.New("boom") })
	Register("secret", func(c *Context) error { c.Println("shh"); return nil })
	Register("help", func(c *Context) error {
		if len(c.Args) > 0 {
			return c.App.HelpFor(c.Args[0], c.Stdout)
		}
		c.App.Help(c.Stdout)
		return nil
	})
}

func testApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	app, err := New([]byte(testManifest))
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	app.Stdin, app.Stdout, app.Stderr = strings.NewReader(""), &out, &errOut
	app.StdinTTY, app.Color = true, false
	return app, &out, &errOut
}

func TestDispatchRunsCommandsAndAliases(t *testing.T) {
	app, out, _ := testApp(t)
	for _, args := range [][]string{{"echo", "hi", "there"}, {"say", "hi", "there"}} {
		out.Reset()
		if err := app.Dispatch(args); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != "hi there\n" {
			t.Errorf("%v printed %q", args, got)
		}
	}
}

func TestFlagsMayFollowPositionals(t *testing.T) {
	app, out, _ := testApp(t)
	if err := app.Dispatch([]string{"echo", "a", "-u", "b", "--times", "2", "--", "-u", "c"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "A B -U C\nA B -U C\n" {
		t.Errorf("got %q", got)
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"echo", "ok"}, 0, ""},
		{[]string{"echo", "-h"}, 0, ""},
		{[]string{"fail"}, 1, "zx: boom"},
		{[]string{"echo", "--bogus"}, 2, "Run 'zx help echo' for usage."},
		{[]string{"nope"}, 2, `unknown command "nope"`},
		{[]string{"ech"}, 2, `Did you mean "echo"?`},
		{[]string{"--bogus"}, 2, "unknown flag --bogus"},
		{[]string{"ghost"}, 2, `unknown command "ghost"`},
	}
	for _, tc := range cases {
		app, _, errOut := testApp(t)
		if code := app.Exit(app.Dispatch(tc.args)); code != tc.code {
			t.Errorf("%v exited %d, want %d", tc.args, code, tc.code)
		}
		if !strings.Contains(errOut.String(), tc.msg) {
			t.Errorf("%v stderr = %q, want it to contain %q", tc.args, errOut.String(), tc.msg)
		}
	}
}

func TestHelpOutput(t *testing.T) {
	app, out, _ := testApp(t)
	if err := app.Dispatch(nil); err != nil {
		t.Fatal(err)
	}
	top := out.String()
	for _, want := range []string{"test build", "Test\n", "echo", "Print the arguments", "(say)"} {
		if !strings.Contains(top, want) {
			t.Errorf("help is missing %q:\n%s", want, top)
		}
	}
	for _, hidden := range []string{"secret", "ghost"} {
		if strings.Contains(top, hidden) {
			t.Errorf("help should not list %q", hidden)
		}
	}

	out.Reset()
	if err := app.Dispatch([]string{"help", "echo"}); err != nil {
		t.Fatal(err)
	}
	detail := out.String()
	for _, want := range []string{"zx echo  Print the arguments", "Aliases\n  say", "-u ", "--times count", "(default 1)", "--copy", "zx echo hi"} {
		if !strings.Contains(detail, want) {
			t.Errorf("command help is missing %q:\n%s", want, detail)
		}
	}
	// -h inside a command prints the same thing and counts as success.
	out.Reset()
	if err := app.Dispatch([]string{"echo", "-h"}); !errors.Is(err, flag.ErrHelp) || out.String() != detail {
		t.Errorf("echo -h: err=%v\n%s", err, out.String())
	}
}

func TestStripCopy(t *testing.T) {
	got, found := stripCopy([]string{"a", "--copy", "b", "--", "--copy"})
	if !found || !reflect.DeepEqual(got, []string{"a", "b", "--", "--copy"}) {
		t.Errorf("stripCopy = %v, %v", got, found)
	}
	if _, found := stripCopy([]string{"a", "-c"}); found {
		t.Error("-c after the command name belongs to the command")
	}
}

func TestStripJSONC(t *testing.T) {
	in := `// leading comment
{
  "url": "https://example.com/a,b", // not a comment inside the string
  /* block
     comment */
  "quote": "she said \"hi\" // still a string",
  "list": [1, 2, 3, /* trailing */ ],
  "obj": {"a": 1,},
}`
	var got map[string]any
	if err := json.Unmarshal(StripJSONC([]byte(in)), &got); err != nil {
		t.Fatalf("stripped output is not JSON: %v\n%s", err, StripJSONC([]byte(in)))
	}
	if got["url"] != "https://example.com/a,b" || got["quote"] != `she said "hi" // still a string` {
		t.Errorf("strings were damaged: %v", got)
	}
	if len(got["list"].([]any)) != 3 {
		t.Errorf("list = %v", got["list"])
	}
	// Plain JSON passes through untouched.
	plain := `{"a":[1,2],"b":"x"}`
	if string(StripJSONC([]byte(plain))) != plain {
		t.Error("plain JSON was changed")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigMergesUserAndProject(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "home", "zxcli.json")
	t.Setenv("ZX_CONFIG", user)
	writeFile(t, user, `{
  "aliases": {"e": "echo -u", "shared": "echo user"},
  "commands": [{"name": "greet", "summary": "from user", "run": "echo user"}],
  "disabled": ["fail"],
  "settings": {"repos.dir": "~/code", "shared": "user"},
}`)
	project := filepath.Join(dir, "work", "repo")
	writeFile(t, filepath.Join(project, ProjectFile), `{
  "aliases": {"shared": "echo project"},
  "settings": {"shared": "project"},
  "commands": [
    {"name": "greet", "run": "echo project"},
    {"name": "bad name", "run": "x"},
    {"name": "norun"}
  ]
}`)

	cfg, warnings := LoadConfig(filepath.Join(project, "src", "deep"))
	if len(warnings) != 2 {
		t.Errorf("want 2 warnings for the two bad commands, got %v", warnings)
	}
	if cfg.ProjectPath != filepath.Join(project, ProjectFile) {
		t.Errorf("ProjectPath = %q", cfg.ProjectPath)
	}
	if cfg.Aliases["e"] != "echo -u" || cfg.Aliases["shared"] != "echo project" {
		t.Errorf("aliases = %v", cfg.Aliases)
	}
	if cfg.Setting("repos.dir", "") != "~/code" || cfg.Setting("shared", "") != "project" || cfg.Setting("unset", "fallback") != "fallback" {
		t.Errorf("settings = %v", cfg.Settings)
	}
	if !cfg.Disabled["fail"] {
		t.Error("fail should be disabled")
	}
	s, ok := cfg.Script("greet")
	if !ok || s.Run != "echo project" || s.Dir != project {
		t.Errorf("project scripts should win and run from the project folder: %+v", s)
	}

	app, out, _ := testApp(t)
	if err := app.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.Dispatch([]string{"e", "quiet"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "QUIET\n" {
		t.Errorf("alias with flags printed %q", out.String())
	}
	if err := app.Dispatch([]string{"fail"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("disabled command: %v", err)
	}
}

func TestLoadConfigSurvivesBrokenFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zxcli.json")
	t.Setenv("ZX_CONFIG", path)
	if _, warnings := LoadConfig(t.TempDir()); len(warnings) != 0 {
		t.Errorf("a missing config is fine, got %v", warnings)
	}
	writeFile(t, path, `{"aliases": `)
	cfg, warnings := LoadConfig(t.TempDir())
	if len(warnings) != 1 || !strings.Contains(warnings[0], path) {
		t.Errorf("a broken config should warn and name the file, got %v", warnings)
	}
	if len(cfg.Aliases) != 0 {
		t.Error("a broken config should contribute nothing")
	}
}

func TestScriptCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script tests use sh")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "marker.txt"), "found me")
	app, out, _ := testApp(t)
	cfg := Config{Project: []Script{
		{Name: "show", Run: "cat marker.txt", Dir: dir},
		{Name: "args", Run: "printf '%s|'"},
		{Name: "swap", Run: `echo "$2 $1"`},
		{Name: "die", Run: "exit 7"},
	}}
	if err := app.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		args []string
		want string
	}{
		{[]string{"show"}, "found me"},                // runs in the project folder
		{[]string{"args", "a b", "c"}, "a b|c|"},      // arguments are appended, quoting intact
		{[]string{"swap", "one", "two"}, "two one\n"}, // or placed where $1/$2 say
	}
	for _, c := range checks {
		out.Reset()
		if err := app.Dispatch(c.args); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if out.String() != c.want {
			t.Errorf("%v printed %q, want %q", c.args, out.String(), c.want)
		}
	}
	if code := app.Exit(app.Dispatch([]string{"die"})); code != 7 {
		t.Errorf("script exit status should pass through, got %d", code)
	}
	out.Reset()
	if err := app.Dispatch(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Project commands") || !strings.Contains(out.String(), "cat marker.txt") {
		t.Errorf("help should list project commands:\n%s", out.String())
	}
}

func TestExternalPlugins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plugin test uses a shell script")
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "zx-hello")
	writeFile(t, plugin, "#!/bin/sh\necho \"plugin got: $*\"\n")
	if err := os.Chmod(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "zx-notexec"), "data")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := ExternalPlugins(); !reflect.DeepEqual(got, []string{"hello"}) {
		t.Errorf("ExternalPlugins = %v, want [hello]", got)
	}
	app, out, _ := testApp(t)
	if err := app.Dispatch([]string{"hello", "x", "y"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "plugin got: x y\n" {
		t.Errorf("plugin printed %q", out.String())
	}
}

func TestManifestProblemsAreReported(t *testing.T) {
	if _, err := New([]byte(`{"commands": [{"name": "echo", "summary": "a"}, {"name": "fail", "summary": "b", "aliases": ["echo"]}]}`)); err == nil {
		t.Error("an alias that collides with a command name should be an error")
	}
	if _, err := New([]byte(`{"commands": [{"name": "echo", "sumary": "typo"}]}`)); err == nil {
		t.Error("unknown manifest fields should be an error")
	}
	if _, err := New([]byte(`not json`)); err == nil {
		t.Error("invalid JSON should be an error")
	}
}
