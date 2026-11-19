package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli/core"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func foundNames(found []foundStack) string {
	var names []string
	for _, f := range found {
		names = append(names, f.spec())
	}
	return strings.Join(names, " ")
}

func minOf(checks []core.DoctorCheck, tool string) string {
	for _, ck := range checks {
		if ck.Tool == tool {
			return ck.Min
		}
	}
	return "<none>"
}

func TestDiscoverRecognisesCommonStacks(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"composer.json":                   `{"require":{"php":"^8.2","laravel/framework":"^11"}}`,
		"artisan":                         "",
		".env.example":                    "APP_KEY=base64:abc\n",
		"Dockerfile":                      "FROM php",
		"api/global.json":                 `{"sdk":{"version":"8.0.100"}}`,
		"api/src/App/App.csproj":          `<Project><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`,
		"api/App.sln":                     "",
		"web/package.json":                `{"dependencies":{"react":"^18"},"engines":{"node":">=20 <23"}}`,
		"web/node_modules/x/package.json": `{"dependencies":{"vue":"1"}}`, // ignored: inside node_modules
		"tools/go.mod":                    "module x\n\ngo 1.22.3\n",
	})
	found := findStacks(dir)
	if got := foundNames(found); got != "laravel docker dotnet@api go@tools react@web" {
		t.Errorf("found %q", got)
	}
	by := map[string][]core.DoctorCheck{}
	for _, f := range found {
		by[f.stack.Name] = f.checks
	}
	if minOf(by["laravel"], "php") != "8.2" || minOf(by["dotnet"], "dotnet") != "8" || minOf(by["react"], "node") != "20" || minOf(by["go"], "go") != "1.22.3" {
		t.Errorf("versions were not read from the project files: %+v", by)
	}
	// The framework hides the language under it.
	for _, f := range found {
		if f.stack.Name == "php" || f.stack.Name == "node" {
			t.Errorf("%s should be hidden behind its framework", f.stack.Name)
		}
	}
	// A folder with nothing familiar finds nothing.
	if got := findStacks(t.TempDir()); len(got) != 0 {
		t.Errorf("empty folder found %v", foundNames(got))
	}
}

func TestStackVersionsFromProjectFiles(t *testing.T) {
	cases := []struct {
		stack string
		files map[string]string
		tool  string
		want  string
	}{
		{"node", map[string]string{".nvmrc": "v18.17.0\n", "package.json": `{"engines":{"node":">=20"}}`}, "node", "18.17.0"},
		{"node", map[string]string{"package.json": `{"engines":{"node":"^20.11 || ^22"}}`}, "node", "20.11"},
		{"node", map[string]string{"package.json": `{}`, ".nvmrc": "lts/*"}, "node", ""},
		{"python", map[string]string{"pyproject.toml": "[project]\nrequires-python = \">=3.10\"\n"}, "python3", "3.10"},
		{"rust", map[string]string{"Cargo.toml": "[package]\nrust-version = \"1.75\"\n"}, "rustc", "1.75"},
		{"java", map[string]string{"pom.xml": "<properties><maven.compiler.release>17</maven.compiler.release></properties>"}, "java", "17"},
		{"java", map[string]string{"build.gradle.kts": "java { toolchain { languageVersion.set(JavaLanguageVersion.of(21)) } }"}, "java", "21"},
		{"ruby", map[string]string{"Gemfile": "source 'https://rubygems.org'\nruby '3.3.0'\n"}, "ruby", "3.3.0"},
		{"go", map[string]string{"go.mod": "module x\n\ngo 1.22\n"}, "go", "1.22"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		writeFiles(t, dir, tc.files)
		checks := findStack(tc.stack).Checks(dir, "")
		if got := minOf(checks, tc.tool); got != tc.want {
			t.Errorf("%s %v: min for %s = %q, want %q", tc.stack, tc.files, tc.tool, got, tc.want)
		}
	}
}

func TestJavaWrappersAndNodePackageManagers(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"pom.xml": "<project/>", "mvnw": "", "build.gradle": "", "pnpm-lock.yaml": "", "package.json": "{}"})
	java := javaChecks(dir, "")
	if minOf(java, "mvn") != "<none>" || minOf(java, "gradle") != "" {
		t.Errorf("mvnw means Maven is not needed, no gradlew means Gradle is: %+v", java)
	}
	node := nodeChecks(dir, "web")
	var fix string
	for _, ck := range node {
		if ck.File == "node_modules" {
			fix = ck.Install.For("linux")
		}
	}
	if minOf(node, "pnpm") != "" || fix != "cd web && pnpm install" {
		t.Errorf("pnpm project: %+v fix=%q", node, fix)
	}
}

func TestFileChecksReportAndFix(t *testing.T) {
	dir := t.TempDir()
	ck := fileCheck("", "vendor", "vendor", "composer install")
	ck.Dir = dir
	got := evalCheck(ck, true)
	if got.OK || got.Status != "missing" || got.Fix != "composer install" {
		t.Errorf("missing folder: %+v", got)
	}
	os.Mkdir(filepath.Join(dir, "vendor"), 0o755)
	if got := evalCheck(ck, true); !got.OK || got.Status != "ok" {
		t.Errorf("present folder: %+v", got)
	}
	// Stack checks are labelled by stack, and a stack's tool replaces the machine's.
	st := core.DoctorCheck{Tool: "php", Stack: "laravel"}
	if got := evalCheck(st, true); got.Source != "stack:laravel" {
		t.Errorf("source = %q", got.Source)
	}
	plan := planWith(core.Config{Settings: map[string]string{"doctor.tools": "php, git"}}, []core.DoctorCheck{{Tool: "php", Min: "8.2", Stack: "laravel"}})
	if len(plan) != 2 || plan[0].Tool != "git" || plan[1].Min != "8.2" {
		t.Errorf("plan = %+v", plan)
	}
	// A project entry beats the stack's.
	plan = planWith(core.Config{Settings: map[string]string{"doctor.tools": "git"}, ProjectDoctor: []core.DoctorCheck{{Tool: "php", Min: "8.4", Project: true}}},
		[]core.DoctorCheck{{Tool: "php", Min: "8.2", Stack: "laravel"}})
	if len(plan) != 2 || plan[1].Min != "8.4" {
		t.Errorf("project should win: %+v", plan)
	}
}

func TestDoctorStackArguments(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	t.Setenv("ZX_CONFIG", filepath.Join(home, "zxcli.json"))
	writeFiles(t, dir, map[string]string{"go.mod": "module x\n\ngo 1.22\n"})
	oldWD := workDir
	workDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { workDir = oldWD })

	sources := func(args ...string) string {
		cfg, _ := core.LoadConfig(dir)
		a, out, _ := newApp(t, "y\n")
		if err := a.SetConfig(cfg); err != nil {
			t.Fatal(err)
		}
		if err := a.Dispatch(append([]string{"doctor", "--json"}, args...)); err != nil {
			t.Fatal(err)
		}
		var checks []Check
		if err := json.Unmarshal(out.Bytes(), &checks); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		var s []string
		for _, ck := range checks {
			if strings.HasPrefix(ck.Source, "stack:") {
				s = append(s, ck.Source+"/"+ck.Name)
			}
		}
		return strings.Join(s, " ")
	}
	want := "stack:go/go"
	for _, args := range [][]string{{"go"}, {"--stack", "golang"}, {"--include=go"}, {"go,go"}} {
		if got := sources(args...); got != want {
			t.Errorf("zx doctor %v: stack checks %q, want %q", args, got, want)
		}
	}
	if got := sources(); got != "" {
		t.Errorf("with no stack, nothing stack-specific should run: %q", got)
	}
	// A saved stack applies without being typed.
	if err := core.SaveSetting(core.ProjectSettingsPath(dir), "doctor.stacks", "go"); err != nil {
		t.Fatal(err)
	}
	if got := sources(); got != want {
		t.Errorf("saved stack: %q", got)
	}
	if _, _, err := run(t, "", "doctor", "larvel"); err == nil || !strings.Contains(err.Error(), "unknown stack") || !strings.Contains(err.Error(), "laravel") {
		t.Errorf("a typo should list the known stacks: %v", err)
	}
}

func TestDiscoverContextHidesSecrets(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".env":               "REAL_SECRET=hunter2\n",
		".env.example":       "APP_KEY=base64:abc123\nDB_HOST=localhost\n# a comment\n",
		"README.md":          "Run it.\nAPI_TOKEN=abcd1234\nstripe_secret: sk_live_zzz\n",
		"docker-compose.yml": "services:\n  redis:\n    image: redis:7\n",
	})
	var all strings.Builder
	for _, f := range gatherContext(dir) {
		all.WriteString("=== " + f.name + "\n" + f.text + "\n")
	}
	text := all.String()
	for _, leak := range []string{"hunter2", "REAL_SECRET", "abc123", "localhost", "abcd1234", "sk_live_zzz"} {
		if strings.Contains(text, leak) {
			t.Errorf("%q must not be sent:\n%s", leak, text)
		}
	}
	for _, want := range []string{"APP_KEY=", "DB_HOST=", "redis:7", "<redacted>"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q should be sent:\n%s", want, text)
		}
	}
}

func TestParseSuggestionsDropsOddEntries(t *testing.T) {
	reply := "Sure!\n" + `{"tools": [
	  {"tool": "redis-server", "min": "v7.2", "why": "docker-compose.yml runs redis", "install": {"darwin": "brew install redis", "linux": "sudo apt-get install -y redis-server\nrm -rf ~", "windows": ""}},
	  {"tool": "git", "why": "already checked"},
	  {"tool": "rm -rf /", "why": "bad name"},
	  {"tool": "redis-server", "why": "duplicate"},
	  {"tool": "psql", "why": "uses postgres"}
	]}`
	got, err := parseSuggestions(reply, "/x", map[string]bool{"git": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Tool != "redis-server" || got[1].Tool != "psql" {
		t.Fatalf("got %+v", got)
	}
	r := got[0]
	if r.Min != "7.2" || r.Stack != "ai" || r.Install.For("darwin") != "brew install redis" || r.Install.For("linux") != "" || got[1].Install != nil {
		t.Errorf("redis-server = %+v", r)
	}
	if _, err := parseSuggestions("no json here", "/x", nil); err == nil {
		t.Error("a reply with no JSON should be an error")
	}
}

func TestDiscoverWithAIAndSave(t *testing.T) {
	ai, _, project, _ := aiFixture(t, true, `{"tools": [{"tool": "redis-server", "min": "7", "why": "docker-compose.yml runs redis", "install": {"darwin": "brew install redis"}}]}`)
	writeFiles(t, project, map[string]string{
		"go.mod":             "module x\n\ngo 1.22\n",
		"docker-compose.yml": "services:\n  redis:\n    image: redis:7\n",
		".env.example":       "SECRET_KEY=topsecretvalue\n",
	})
	cfg, _ := core.LoadConfig(project)
	a, out, errOut := newApp(t, "y\ny\n")
	a.StdinTTY = true // the prompts need a person; the answers come from the test
	if err := a.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := a.Dispatch([]string{"doctor", "--discover"}); err != nil {
		t.Fatal(err)
	}
	if len(ai.prompts) != 1 || !strings.Contains(ai.prompts[0], "docker-compose.yml") || strings.Contains(ai.prompts[0], "topsecretvalue") {
		t.Errorf("the prompt should carry the project files and no secret values:\n%v", ai.prompts)
	}
	if !strings.Contains(errOut.String(), "It would send") && !strings.Contains(out.String(), "It would send") {
		t.Errorf("zx must say what it sends:\nout=%s\nerr=%s", out, errOut)
	}
	text := out.String()
	for _, want := range []string{"Found:", "go  ", "Stack: go", "Suggested by AI", "redis-server"} {
		if !strings.Contains(text, want) {
			t.Errorf("report is missing %q:\n%s", want, text)
		}
	}
	// Saved: the stack as a setting, the suggestion as a doctor file.
	if got := core.ReadSettings(core.ProjectSettingsPath(project))["doctor.stacks"]; got != "go,docker" { // docker-compose.yml is recognised too
		t.Errorf("doctor.stacks = %q", got)
	}
	saved, _ := core.LoadDoctorFile(filepath.Join(project, ".zx", core.DoctorFileName), project)
	if len(saved) != 1 || saved[0].Tool != "redis-server" || saved[0].Min != "7" || saved[0].Install.For("darwin") != "brew install redis" {
		t.Errorf("saved doctor file = %+v", saved)
	}
	// A later plain "zx doctor" picks both up.
	cfg, _ = core.LoadConfig(project)
	plan := planWith(cfg, nil)
	if !hasTool(plan, "redis-server") {
		t.Errorf("the saved suggestion should be in the plan: %+v", plan)
	}

	// Declining the question sends nothing.
	ai2, _, project2, _ := aiFixture(t, true)
	writeFiles(t, project2, map[string]string{"go.mod": "module x\n\ngo 1.22\n", "README.md": "hello"})
	cfg2, _ := core.LoadConfig(project2)
	a2, _, _ := newApp(t, "n\nn\n")
	a2.StdinTTY = true
	a2.SetConfig(cfg2)
	if err := a2.Dispatch([]string{"doctor", "--discover"}); err != nil {
		t.Fatal(err)
	}
	if len(ai2.prompts) != 0 || fileExists(filepath.Join(project2, ".zx", core.DoctorFileName)) {
		t.Error("answering no must send nothing and save nothing")
	}
	// --no-ai never asks.
	ai3, _, project3, _ := aiFixture(t, true)
	writeFiles(t, project3, map[string]string{"go.mod": "module x\n\ngo 1.22\n", "README.md": "hello"})
	cfg3, _ := core.LoadConfig(project3)
	a3, _, _ := newApp(t, "")
	a3.SetConfig(cfg3)
	if err := a3.Dispatch([]string{"doctor", "--discover", "--no-ai"}); err != nil || len(ai3.prompts) != 0 {
		t.Errorf("--no-ai: %v, prompts=%d", err, len(ai3.prompts))
	}
}
