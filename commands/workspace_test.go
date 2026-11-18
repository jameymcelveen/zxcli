package commands

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func TestParseStatusV2(t *testing.T) {
	out := `# branch.oid 1234567890abcdef
# branch.head feature/x
# branch.upstream origin/feature/x
# branch.ab +2 -3
1 .M N... 100644 100644 100644 aaa bbb file.go
1 A. N... 000000 100644 100644 000 ccc new.go
2 R. N... 100644 100644 100644 ddd eee R100 b.go	a.go
u UU N... 100644 100644 100644 100644 f g h conflict.go
? scratch.txt
? notes/
! ignored.log
`
	var r RepoStatus
	parseStatusV2(out, &r)
	want := RepoStatus{Branch: "feature/x", Upstream: "origin/feature/x", Ahead: 2, Behind: 3, Changed: 4, Untracked: 2}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("got %+v\nwant %+v", r, want)
	}
}

func TestRepoProblems(t *testing.T) {
	cases := []struct {
		r    RepoStatus
		want string
	}{
		{RepoStatus{Branch: "main", Upstream: "origin/main", HasRemote: true}, ""},
		{RepoStatus{Branch: "main", Upstream: "origin/main", HasRemote: true, Changed: 2, Untracked: 1, Ahead: 3, Behind: 4}, "2 changed, 1 untracked, 3 unpushed, 4 behind"},
		{RepoStatus{Branch: "main"}, "no remote"},
		{RepoStatus{Branch: "wip", HasRemote: true}, "branch not pushed"},
		{RepoStatus{Branch: "(detached)", HasRemote: true}, ""},
		{RepoStatus{Error: "git status failed", Changed: 9}, "git status failed"},
	}
	for _, tc := range cases {
		if got := strings.Join(tc.r.Problems(), ", "); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.r, got, tc.want)
		}
	}
}

// gitIn runs git in dir with a fixed identity, and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestReposAgainstRealRepositories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	mk := func(name string) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	write := func(dir, name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A bare "remote", and a clone of it that is clean and fully pushed.
	remote := mk("remote.git")
	gitIn(t, remote, "init", "-q", "--bare", "-b", "main")
	clean := mk("Clean")
	gitIn(t, clean, "init", "-q", "-b", "main")
	write(clean, "a.txt")
	gitIn(t, clean, "add", "-A")
	gitIn(t, clean, "commit", "-q", "-m", "one")
	gitIn(t, clean, "remote", "add", "origin", remote)
	gitIn(t, clean, "push", "-q", "-u", "origin", "main")

	// A second clone with one unpushed commit, one edit and one new file.
	busy := mk("busy")
	gitIn(t, root, "clone", "-q", remote, busy)
	write(busy, "b.txt")
	gitIn(t, busy, "add", "-A")
	gitIn(t, busy, "commit", "-q", "-m", "two")
	if err := os.WriteFile(filepath.Join(busy, "a.txt"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(busy, "untracked.txt")

	// A repo that was never given a remote, and a folder that is not a repo.
	gitIn(t, mk("local-only"), "init", "-q", "-b", "main")
	mk("not-a-repo")

	out := mustRun(t, "", "repos", root)
	for _, want := range []string{"busy", "1 changed, 1 untracked, 1 unpushed", "local-only", "no remote", "3 repos in", "2 need attention"} {
		if !strings.Contains(out, want) {
			t.Errorf("zx repos is missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Clean") || strings.Contains(out, "not-a-repo") || strings.Contains(out, "remote.git") {
		t.Errorf("clean repos, plain folders and bare repos should not be listed:\n%s", out)
	}
	if all := mustRun(t, "", "repos", root, "-a"); !strings.Contains(all, "Clean") || !strings.Contains(all, "clean") {
		t.Errorf("-a should list clean repos:\n%s", all)
	}

	var got []RepoStatus
	if err := json.Unmarshal([]byte(mustRun(t, "", "repos", root, "--json")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "busy" || got[0].Ahead != 1 || got[0].Branch != "main" || !got[0].HasRemote {
		t.Errorf("--json = %+v", got)
	}

	// Pointing at a single repo reports on just that repo, and the folder
	// can come from the settings block.
	if one := mustRun(t, "", "repos", busy); !strings.Contains(one, "1 repo in") || !strings.Contains(one, "1 needs attention") {
		t.Errorf("single repo:\n%s", one)
	}
	app, buf, _ := newApp(t, "")
	if err := app.SetConfig(core.Config{Settings: map[string]string{"repos.dir": root}}); err != nil {
		t.Fatal(err)
	}
	if err := app.Dispatch([]string{"repos"}); err != nil || !strings.Contains(buf.String(), "2 need attention") {
		t.Errorf("repos.dir setting: err=%v\n%s", err, buf.String())
	}
}

func TestInitDetectsKindAndNeverOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"go.mod": "module x\n", "README.md": "mine\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dry := mustRun(t, "", "init", dir, "--dry-run", "--no-git")
	if !strings.Contains(dry, "go project") || !strings.Contains(dry, "would write Makefile") || !strings.Contains(dry, "kept        README.md") {
		t.Errorf("dry run:\n%s", dry)
	}
	if fileExists(filepath.Join(dir, "Makefile")) {
		t.Fatal("--dry-run wrote a file")
	}

	out := mustRun(t, "", "init", dir, "--owner", "Ada Lovelace", "--no-git")
	for _, f := range []string{".editorconfig", ".gitignore", ".zx.json", "LICENSE", "Makefile", filepath.Join(".github", "workflows", "ci.yml")} {
		if !fileExists(filepath.Join(dir, f)) {
			t.Errorf("init did not write %s:\n%s", f, out)
		}
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if read("README.md") != "mine\n" {
		t.Error("init overwrote an existing README")
	}
	license := read("LICENSE")
	if !strings.Contains(license, "Copyright (c) "+strconv.Itoa(time.Now().Year())+" Ada Lovelace") {
		t.Errorf("license header is wrong:\n%s", license[:80])
	}
	if mk := read("Makefile"); !strings.Contains(mk, "\tgo test ./...") || !strings.Contains(mk, "check: lint test") {
		t.Errorf("Makefile:\n%s", mk)
	}
	if !strings.Contains(read(".gitignore"), "coverage.out") || !strings.Contains(read(".gitignore"), ".DS_Store") {
		t.Error(".gitignore is missing the Go or the common entries")
	}

	// The generated .zx.json has to load, and a second run changes nothing.
	t.Setenv("ZX_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	cfg, warnings := core.LoadConfig(dir)
	if s, ok := cfg.Script("check"); len(warnings) > 0 || !ok || s.Run != "make check" {
		t.Errorf("generated .zx.json: %+v %v", s, warnings)
	}
	if again := mustRun(t, "", "init", dir, "--no-git"); strings.Contains(again, "wrote") {
		t.Errorf("a second init should keep everything:\n%s", again)
	}
}

func TestInitKinds(t *testing.T) {
	cases := map[string]string{"App.sln": "dotnet", "Api.csproj": "dotnet", "package.json": "node", "notes.txt": "plain"}
	for file, want := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := DetectKind(dir); got != want {
			t.Errorf("DetectKind with %s = %s, want %s", file, got, want)
		}
	}
	plain := t.TempDir()
	if _, _, err := InitProject(plain, InitOptions{NoGit: true}); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(plain, "Makefile")) || fileExists(filepath.Join(plain, ".github")) {
		t.Error("a plain project should get no Makefile or workflow")
	}
	node := t.TempDir()
	if _, _, err := InitProject(node, InitOptions{Kind: "node", NoCI: true, NoLicense: true, NoGit: true}); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(node, "LICENSE")) || fileExists(filepath.Join(node, ".github")) || !fileExists(filepath.Join(node, "Makefile")) {
		t.Error("--no-ci and --no-license were not honored")
	}
	if _, _, err := run(t, "", "init", t.TempDir(), "--kind", "cobol"); err == nil {
		t.Error("an unknown kind should fail")
	}
}

func TestInitStartsAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := filepath.Join(t.TempDir(), "fresh")
	out := mustRun(t, "", "init", dir)
	if !fileExists(filepath.Join(dir, ".git")) || !strings.Contains(out, "started     git repository") {
		t.Errorf("init should create the folder and start a repo:\n%s", out)
	}
}

func TestDoctor(t *testing.T) {
	app, buf, _ := newApp(t, "")
	if err := app.SetConfig(core.Config{Settings: map[string]string{"doctor.tools": "go, zx-no-such-tool"}}); err != nil {
		t.Fatal(err)
	}
	if err := app.Dispatch([]string{"doctor"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "ok       go") || !strings.Contains(out, "missing  zx-no-such-tool  not installed") || !strings.Contains(out, "1 of 2 checks need attention") {
		t.Errorf("doctor output:\n%s", out)
	}

	buf.Reset()
	var exit *core.ExitError
	if err := app.Dispatch([]string{"doctor", "--strict", "--json"}); !errors.As(err, &exit) || exit.Code != 1 {
		t.Errorf("--strict with a missing tool should exit 1, got %v", err)
	}
	var checks []Check
	if err := json.Unmarshal(buf.Bytes(), &checks); err != nil {
		t.Fatalf("--json: %v\n%s", err, buf.String())
	}
	if len(checks) != 2 || !checks[0].OK || checks[0].Path == "" || checks[1].OK {
		t.Errorf("--json = %+v", checks)
	}
	if v := CheckTool("go").Detail; !versionNumber.MatchString(v) {
		t.Errorf("go version detail = %q", v)
	}
}

func TestJSONFlags(t *testing.T) {
	var now struct {
		ISO    string `json:"iso"`
		Epoch  int64  `json:"epoch"`
		Millis int64  `json:"millis"`
	}
	if err := json.Unmarshal([]byte(mustRun(t, "", "now", "1700000000", "--json")), &now); err != nil {
		t.Fatal(err)
	}
	if now.ISO != "2023-11-14T22:13:20Z" || now.Epoch != 1700000000 || now.Millis != 1700000000000 {
		t.Errorf("now --json = %+v", now)
	}
	var ports []Listener
	out, _, err := run(t, "", "port", "--json")
	if err != nil {
		t.Skipf("no port lookup tool here: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &ports); err != nil {
		t.Errorf("port --json is not a JSON array: %v\n%s", err, out)
	}
}

func TestClipAndPasteRoundTrip(t *testing.T) {
	const text = "zx clipboard test ✓"
	previous, pasteErr := core.Paste()
	if _, _, err := run(t, text+"\n", "clip"); err != nil {
		t.Skipf("no clipboard on this machine: %v", err)
	}
	if pasteErr == nil {
		defer core.Copy(previous) // put the user's clipboard back
	}
	if got := mustRun(t, "", "paste"); got != text {
		t.Errorf("paste = %q, want %q", got, text)
	}
}
