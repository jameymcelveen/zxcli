package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
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

// Shell lines with quotes and redirection must survive on every OS. This is
// the one script test that also runs on Windows, where cmd.exe is the shell.
func TestShellLinesKeepTheirQuotes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out file.txt")
	app, out, _ := testApp(t)
	cfg := Config{User: []Script{
		{Name: "write", Run: `echo hello > "` + target + `"`},
		{Name: "speak", Run: `echo "one two"`},
	}}
	if err := app.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.Dispatch([]string{"write"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(data), "hello") {
		t.Errorf("the redirect to a quoted path with a space failed: %v %q", err, data)
	}
	if err := app.Dispatch([]string{"speak"}); err != nil || !strings.Contains(out.String(), "one two") {
		t.Errorf("quoted text was lost: %v %q", err, out.String())
	}
	if err := RunCommand(ShellCommand("exit 5", dir), strings.NewReader(""), out, out); err == nil {
		t.Error("a failing line should report its exit status")
	}
}

func TestCommandFilesAndProjectRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZX_CONFIG", filepath.Join(home, "zxcli.json"))
	root := t.TempDir()
	deep := filepath.Join(root, "src", "pkg")
	writeFile(t, filepath.Join(deep, "x.txt"), "")

	// A .zx folder alone marks the project root; no .zx.json is needed.
	if got := FindProjectRoot(deep); got != "" {
		t.Fatalf("no project yet, got %q", got)
	}
	writeFile(t, filepath.Join(root, ".zx", "commands", "hello.js"), "// summary: Say hello from a file\nconsole.log('hello ' + process.argv.slice(2).join(' ') + ' from ' + process.cwd());\n")
	writeFile(t, filepath.Join(root, ".zx", "commands", "notes.txt"), "not a command")
	writeFile(t, filepath.Join(root, ".zx", "commands", "bad name.sh"), "echo no")
	writeFile(t, filepath.Join(home, "commands", "mine.sh"), "#!/bin/sh\n# summary: A user command\necho mine\n")
	if got := FindProjectRoot(deep); got != root {
		t.Fatalf("FindProjectRoot = %q, want %q", got, root)
	}

	cfg, warnings := LoadConfig(deep)
	if len(warnings) > 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if cfg.ProjectDir != root || cfg.ProjectPath != "" {
		t.Errorf("ProjectDir = %q, ProjectPath = %q", cfg.ProjectDir, cfg.ProjectPath)
	}
	if len(cfg.Project) != 1 || cfg.Project[0].Name != "hello" || cfg.Project[0].Summary != "Say hello from a file" || cfg.Project[0].Dir != root {
		t.Fatalf("project commands = %+v", cfg.Project)
	}
	if len(cfg.User) != 1 || cfg.User[0].Name != "mine" || cfg.User[0].Summary != "A user command" || cfg.User[0].Dir != "" {
		t.Fatalf("user commands = %+v", cfg.User)
	}

	app, out, _ := testApp(t)
	if err := app.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.Dispatch(nil); err != nil || !strings.Contains(out.String(), "Say hello from a file") || !strings.Contains(out.String(), "A user command") {
		t.Errorf("help should list file commands with their summaries: %v\n%s", err, out.String())
	}
	if _, err := exec.LookPath("node"); err == nil {
		out.Reset()
		if err := app.Dispatch([]string{"hello", "big", "world"}); err != nil {
			t.Fatal(err)
		}
		realRoot, _ := filepath.EvalSymlinks(root)
		if got := strings.TrimSpace(out.String()); got != "hello big world from "+realRoot && got != "hello big world from "+root {
			t.Errorf("file command printed %q", got)
		}
	}
	if runtime.GOOS != "windows" {
		out.Reset()
		if err := app.Dispatch([]string{"mine"}); err != nil || out.String() != "mine\n" {
			t.Errorf("user sh command: %v %q", err, out.String())
		}
	}

	// A config entry with the same name wins over a file.
	writeFile(t, filepath.Join(root, ProjectFile), `{ "commands": [ { "name": "hello", "run": "echo from-config" } ] }`)
	cfg, _ = LoadConfig(deep)
	if s, _ := cfg.Script("hello"); s.Run != "echo from-config" || len(cfg.Project) != 1 {
		t.Errorf("config should win: %+v", cfg.Project)
	}
}

func TestFileRunnerReportsWhatIsMissing(t *testing.T) {
	old := LookPath
	defer func() { LookPath = old }()
	LookPath = func(string) (string, error) { return "", errors.New("not found") }
	for file, want := range map[string]string{"a.ts": "TypeScript", "a.js": "Node.js", "a.py": "python3", "a.ps1": "pwsh", "a.rb": "does not know"} {
		if _, err := FileRunner(file); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("FileRunner(%s) = %v, want a message about %q", file, err, want)
		}
	}
	LookPath = func(name string) (string, error) {
		if name == "bun" || name == "pwsh" {
			return "/x/" + name, nil
		}
		return "", errors.New("not found")
	}
	if got, err := FileRunner("a.ts"); err != nil || strings.Join(got, " ") != "bun run" {
		t.Errorf("ts with bun: %v %v", got, err)
	}
	if got, err := FileRunner("a.PS1"); err != nil || got[0] != "pwsh" || got[len(got)-1] != "-File" {
		t.Errorf("ps1: %v %v", got, err)
	}
	if langs := Languages(); !reflect.DeepEqual(langs, []string{"sh", "ps1", "js", "ts"}) && runtime.GOOS != "windows" {
		t.Errorf("Languages() = %v", langs)
	}
}

func TestSettingsFilesLayerOverConfig(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("ZX_CONFIG", filepath.Join(home, "zxcli.json"))
	writeFile(t, filepath.Join(home, "zxcli.json"), `{ "settings": { "a": "config", "b": "config", "c": "config", "d": "config" } }`)
	writeFile(t, filepath.Join(root, ProjectFile), `{ "settings": { "c": "project-config", "d": "project-config" } }`)
	if err := SaveSetting(UserSettingsPath(), "b", "user-saved"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSetting(UserSettingsPath(), "c", "user-saved"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSetting(ProjectSettingsPath(root), "d", "project-saved"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadConfig(root)
	want := map[string]string{"a": "config", "b": "user-saved", "c": "project-config", "d": "project-saved"}
	if !reflect.DeepEqual(cfg.Settings, want) {
		t.Errorf("settings = %v\nwant %v", cfg.Settings, want)
	}
	// Saving never touches the hand-written config, and "" removes a key.
	if data, _ := os.ReadFile(filepath.Join(home, "zxcli.json")); !strings.Contains(string(data), `"a": "config"`) {
		t.Error("the config file was rewritten")
	}
	if err := SaveSetting(UserSettingsPath(), "b", ""); err != nil {
		t.Fatal(err)
	}
	if got := ReadSettings(UserSettingsPath()); got["b"] != "" || got["c"] != "user-saved" {
		t.Errorf("after removing b: %v", got)
	}
}

func TestSentencesGoToTheAI(t *testing.T) {
	app, _, _ := testApp(t)
	// The test binary has no AI command, so the sentence shows up as the
	// hidden "__ask" command being unknown rather than as a typo of "echo".
	err := app.Dispatch([]string{"echo the time please"})
	if err == nil || !strings.Contains(err.Error(), "__ask") {
		t.Errorf("a sentence should be routed to __ask, got %v", err)
	}
}
