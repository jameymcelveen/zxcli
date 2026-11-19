package commands

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli/core"
)

func TestPatternMessages(t *testing.T) {
	cases := []struct {
		name      string
		files     []change
		subject   string
		body      string
		confident bool
	}{
		{"one edited file", []change{{Path: "README.md", Status: "M", Added: 200, Deleted: 55}}, "Update README.md", "+200 -55", true},
		{"one new file", []change{{Path: "a/b.go", Status: "A", Added: 10}}, "Add a/b.go", "+10 -0", true},
		{"one deleted file", []change{{Path: "old.txt", Status: "D", Deleted: 4}}, "Delete old.txt", "+0 -4", true},
		{"binary", []change{{Path: "logo.png", Status: "M", Binary: true}}, "Update logo.png", "binary file", true},
		{"two files", []change{{Path: "a.go", Status: "M", Added: 1, Deleted: 1}, {Path: "b.go", Status: "A", Added: 5}}, "Add b.go, update a.go", "a.go  +1 -1\nb.go  +5 -0", true},
		{"many in one folder", []change{
			{Path: "site/a.html", Status: "M"}, {Path: "site/b.css", Status: "M"}, {Path: "site/c/d.js", Status: "M"}, {Path: "site/e.js", Status: "M"},
		}, "Update 4 files in site/", "4 files changed, +0 -0\n\nM site/a.html\nM site/b.css\nM site/c/d.js\nM site/e.js", true},
		{"many everywhere", []change{
			{Path: "a.go", Status: "M"}, {Path: "docs/b.md", Status: "A"}, {Path: "x/y.go", Status: "D"}, {Path: "z.txt", Status: "M"},
		}, "Change 4 files", "", false},
	}
	for _, tc := range cases {
		s, b, conf := patternMessage(tc.files)
		if s != tc.subject || conf != tc.confident || (tc.body != "" && b != tc.body) {
			t.Errorf("%s: got %q / %q / %v, want %q / %q / %v", tc.name, s, b, conf, tc.subject, tc.body, tc.confident)
		}
	}
}

func TestCleanCommitReply(t *testing.T) {
	s, b, err := cleanCommitReply("```\n\"Add retry to the uploader.\"\n\nRetries three times with backoff.\n```\n")
	if err != nil || s != "Add retry to the uploader" || b != "Retries three times with backoff." {
		t.Errorf("got %q / %q / %v", s, b, err)
	}
	long := strings.Repeat("word ", 30)
	s, b, _ = cleanCommitReply(long)
	if len(s) > 72 || b == "" {
		t.Errorf("a long subject should be cut at 72 and the rest moved to the body: %q / %q", s, b)
	}
	if _, _, err := cleanCommitReply("  \n"); err == nil {
		t.Error("an empty reply should be an error")
	}
}

func TestNextTag(t *testing.T) {
	cases := []struct {
		tags []string
		arg  string
		want string
	}{
		{[]string{"v0.5.1", "v0.6.0", "v0.10.0", "notes"}, "", "v0.10.1"},
		{[]string{"v0.6.0"}, "minor", "v0.7.0"},
		{[]string{"v0.6.0"}, "major", "v1.0.0"},
		{[]string{"1.2.3"}, "patch", "1.2.4"},
		{[]string{"v1.2.3"}, "2.0.0", "v2.0.0"},
		{nil, "", "v0.1.0"},
		{nil, "major", "v1.0.0"},
	}
	for _, tc := range cases {
		got, err := nextTag(tc.tags, tc.arg)
		if err != nil || got != tc.want {
			t.Errorf("nextTag(%v, %q) = %q, %v; want %q", tc.tags, tc.arg, got, err, tc.want)
		}
	}
	if _, err := nextTag(nil, "banana"); err == nil {
		t.Error("a word that is not a version should be refused")
	}
}

func TestRiskyFiles(t *testing.T) {
	got := riskyFiles([]change{
		{Path: ".env", Status: "A"}, {Path: "web/.env.local", Status: "M"}, {Path: ".env.example", Status: "A"},
		{Path: "keys/server.pem", Status: "A"}, {Path: "id_rsa.pub", Status: "A"}, {Path: "main.go", Status: "A"}, {Path: ".env", Status: "D"},
	})
	if strings.Join(got, ",") != ".env,web/.env.local,keys/server.pem" {
		t.Errorf("got %v", got)
	}
}

// gitFixture is a throwaway repository with a bare remote, git told who the
// author is, and no user git config getting in the way.
type gitFixture struct {
	dir, remote string
	run         func(settings map[string]string, stdin string, args ...string) (string, string, error)
	ai          *fakeAI
}

func newGitFixture(t *testing.T, replies ...string) *gitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com",
		"GIT_CONFIG_GLOBAL": filepath.Join(root, "gitconfig"), "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
	f := &gitFixture{dir: filepath.Join(root, "work"), remote: filepath.Join(root, "remote.git"), ai: &fakeAI{replies: replies}}
	for _, d := range []string{f.dir, f.remote} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, f.remote, "init", "-q", "--bare", "-b", "main")
	gitIn(t, f.dir, "init", "-q", "-b", "main")
	gitIn(t, f.dir, "remote", "add", "origin", f.remote)

	oldProviders, oldWD := aiProviders, workDir
	aiProviders = []aiProvider{{
		ID: "fake", Label: "Stand-in",
		Detect: func(core.Config) (bool, string) { return true, "here" },
		Ask: func(_ core.Config, prompt string) (string, error) {
			f.ai.prompts = append(f.ai.prompts, prompt)
			if len(f.ai.replies) == 0 {
				return "", errors.New("nothing left to say")
			}
			r := f.ai.replies[0]
			f.ai.replies = f.ai.replies[1:]
			return r, nil
		},
	}}
	workDir = func() (string, error) { return f.dir, nil }
	t.Cleanup(func() { aiProviders, workDir = oldProviders, oldWD })

	f.run = func(settings map[string]string, stdin string, args ...string) (string, string, error) {
		app, out, errOut := newApp(t, stdin)
		if err := app.SetConfig(core.Config{Settings: settings}); err != nil {
			t.Fatal(err)
		}
		err := app.Dispatch(args)
		return out.String(), errOut.String(), err
	}
	return f
}

func (f *gitFixture) write(t *testing.T, name, content string) {
	t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *gitFixture) git(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", f.dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestGitSaveWritesPatternMessages(t *testing.T) {
	f := newGitFixture(t)
	f.write(t, "README.md", "one\n")
	out, _, err := f.run(nil, "", "git", "save")
	if err != nil || !strings.Contains(out, "committed") {
		t.Fatalf("save: %v\n%s", err, out)
	}
	if got := f.git(t, "log", "-1", "--format=%B"); got != "Add README.md\n\n+1 -0" {
		t.Errorf("first commit message = %q", got)
	}
	f.write(t, "README.md", "one\ntwo\nthree\n")
	if _, _, err := f.run(nil, "", "git", "save"); err != nil {
		t.Fatal(err)
	}
	if got := f.git(t, "log", "-1", "--format=%B"); got != "Update README.md\n\n+2 -0" {
		t.Errorf("second commit message = %q", got)
	}
	out, _, err = f.run(nil, "", "git", "save")
	if err != nil || !strings.Contains(out, "nothing to commit") {
		t.Errorf("a clean tree should say so: %v %q", err, out)
	}
	if len(f.ai.prompts) != 0 {
		t.Error("a simple change should never ask the AI")
	}
}

func TestGitSaveMessageFromYouAndDryRun(t *testing.T) {
	f := newGitFixture(t)
	f.write(t, "a.txt", "x\n")
	out, _, err := f.run(nil, "", "git", "save", "-n")
	if err != nil || !strings.Contains(out, "would commit: Add a.txt") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if got := f.git(t, "status", "--porcelain"); !strings.HasPrefix(got, "??") {
		t.Errorf("a dry run must not stage anything, status is %q", got)
	}
	if _, _, err := f.run(nil, "", "git", "save", "fix", "the", "thing"); err != nil {
		t.Fatal(err)
	}
	if got := f.git(t, "log", "-1", "--format=%s"); got != "fix the thing" {
		t.Errorf("subject = %q", got)
	}
}

func TestGitSaveFallsBackOnAI(t *testing.T) {
	f := newGitFixture(t, "Add the starter pages and tidy the folders\n\nGroups the demo files together.")
	for _, n := range []string{"a.go", "docs/b.md", "x/y.go", "z.txt"} {
		f.write(t, n, "hi\n")
	}
	// Auto mode, no AI chosen yet: a plain message, and the AI is left alone.
	out, errOut, err := f.run(nil, "", "git", "save", "-n")
	if err != nil || !strings.Contains(out, "would commit: Add 4 files") || len(f.ai.prompts) != 0 {
		t.Fatalf("without a chosen AI: %v\n%s\n%s", err, out, errOut)
	}
	// With an AI chosen, the mixed change goes to it.
	out, _, err = f.run(map[string]string{"ai.provider": "fake"}, "", "git", "save")
	if err != nil || len(f.ai.prompts) != 1 {
		t.Fatalf("with an AI: %v\n%s", err, out)
	}
	if got := f.git(t, "log", "-1", "--format=%B"); !strings.HasPrefix(got, "Add the starter pages and tidy the folders\n\nGroups the demo files") {
		t.Errorf("message = %q", got)
	}
	if !strings.Contains(f.ai.prompts[0], "a.go") || strings.Contains(f.ai.prompts[0], "ghp_") {
		t.Errorf("the prompt should list the files: %s", f.ai.prompts[0])
	}
}

func TestGitSaveAIFailureFallsBack(t *testing.T) {
	f := newGitFixture(t) // the stand-in has nothing to say, so it errors
	for _, n := range []string{"a.go", "docs/b.md", "x/y.go", "z.txt"} {
		f.write(t, n, "hi\n")
	}
	_, errOut, err := f.run(map[string]string{"ai.provider": "fake"}, "", "git", "save", "--ai")
	if err != nil || !strings.Contains(errOut, "Using a plain one") {
		t.Fatalf("a failing AI must not lose the commit: %v\n%s", err, errOut)
	}
	if got := f.git(t, "log", "-1", "--format=%s"); got != "Add 4 files" {
		t.Errorf("subject = %q", got)
	}
}

func TestGitSaveRefusesSecrets(t *testing.T) {
	f := newGitFixture(t)
	f.write(t, ".env", "TOKEN=abc\n")
	f.write(t, "main.go", "package main\n")
	_, _, err := f.run(nil, "", "git", "save")
	if err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("expected a refusal naming .env, got %v", err)
	}
	if got := f.git(t, "status", "--porcelain"); !strings.Contains(got, "?? .env") {
		t.Errorf(".env should be left unstaged: %q", got)
	}
	if _, _, err := f.run(nil, "", "git", "save", "--force"); err != nil {
		t.Fatalf("--force should commit it: %v", err)
	}
}

func TestGitShipUndoAndSync(t *testing.T) {
	f := newGitFixture(t)
	f.write(t, "a.txt", "1\n")
	if _, _, err := f.run(nil, "", "git", "ship", "first"); err != nil {
		t.Fatal(err)
	}
	if got := f.git(t, "rev-parse", "--abbrev-ref", "@{u}"); got != "origin/main" {
		t.Errorf("ship should push and set the upstream, got %q", got)
	}
	f.write(t, "b.txt", "2\n")
	if _, _, err := f.run(nil, "", "git", "ship", "second"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.run(nil, "", "git", "undo"); err == nil || !strings.Contains(err.Error(), "already pushed") {
		t.Errorf("undoing a pushed commit should be refused, got %v", err)
	}
	f.write(t, "c.txt", "3\n")
	if _, _, err := f.run(nil, "", "git", "save", "third"); err != nil {
		t.Fatal(err)
	}
	if out, _, err := f.run(nil, "", "git", "undo"); err != nil || !strings.Contains(out, "third") {
		t.Fatalf("undo: %v %s", err, out)
	}
	if got := f.git(t, "status", "--porcelain"); got != "A  c.txt" {
		t.Errorf("undo should keep the change staged, got %q", got)
	}
	if _, _, err := f.run(nil, "", "git", "save", "fourth"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.run(nil, "", "git", "sync"); err != nil {
		t.Fatal(err)
	}
	if got := f.git(t, "rev-list", "--count", "@{u}..HEAD"); got != "0" {
		t.Errorf("sync should leave nothing unpushed, got %s", got)
	}
}

func TestGitCustomFlow(t *testing.T) {
	f := newGitFixture(t)
	f.write(t, "a.txt", "1\n")
	// A flow of your own: test, commit, push.
	_, _, err := f.run(map[string]string{"git.flow.land": "test, save, push", "git.test": "echo tests-ran"}, "", "git", "land", "ready")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.git(t, "log", "-1", "--format=%s"); got != "ready" {
		t.Errorf("subject = %q", got)
	}
	if got := f.git(t, "ls-remote", f.remote, "refs/heads/main"); got == "" {
		t.Error("the flow should have pushed")
	}
	if _, _, err := f.run(map[string]string{"git.flow.bad": "save, dance"}, "", "git", "bad"); err == nil || !strings.Contains(err.Error(), "dance") {
		t.Errorf("an unknown step should be named in the error, got %v", err)
	}
}

func TestGitTagTestsFirst(t *testing.T) {
	f := newGitFixture(t)
	f.write(t, "a.txt", "1\n")
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", "one")
	f.git(t, "tag", "v0.2.0")
	f.git(t, "push", "-q", "-u", "origin", "main")

	failing := map[string]string{"git.test": "exit 1"}
	if _, _, err := f.run(failing, "", "git", "tag"); err == nil || !strings.Contains(err.Error(), "nothing was tagged") {
		t.Fatalf("failing tests must stop the tag, got %v", err)
	}
	if f.git(t, "tag", "--list", "v0.2.1") != "" {
		t.Fatal("no tag should exist after failing tests")
	}
	out, _, err := f.run(map[string]string{"git.test": "echo ok"}, "", "git", "tag", "-n")
	if err != nil || !strings.Contains(out, "would tag v0.2.1") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if _, _, err := f.run(map[string]string{"git.test": "echo ok"}, "", "git", "tag", "minor"); err != nil {
		t.Fatal(err)
	}
	if got := f.git(t, "ls-remote", "--tags", f.remote, "v0.3.0"); got == "" {
		t.Error("the tag should be pushed")
	}
	f.write(t, "b.txt", "2\n")
	if _, _, err := f.run(nil, "", "git", "tag"); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("a dirty tree should be refused, got %v", err)
	}
}

func TestGitMerge(t *testing.T) {
	f := newGitFixture(t)
	f.write(t, "a.txt", "1\n")
	f.git(t, "add", "-A")
	f.git(t, "commit", "-q", "-m", "base")
	f.git(t, "push", "-q", "-u", "origin", "main")
	f.git(t, "switch", "-q", "-c", "feature")
	f.write(t, "b.txt", "2\n")
	if _, _, err := f.run(nil, "", "git", "save", "add b"); err != nil {
		t.Fatal(err)
	}
	out, _, err := f.run(nil, "", "git", "merge", "--delete")
	if err != nil || !strings.Contains(out, "merged feature into main") {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	if got := f.git(t, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("should end on main, got %s", got)
	}
	if f.git(t, "branch", "--list", "feature") != "" {
		t.Error("--delete should remove the branch")
	}
	if got := f.git(t, "ls-tree", "--name-only", "origin/main"); !strings.Contains(got, "b.txt") {
		t.Errorf("main should be pushed with the merge, tree: %s", got)
	}
	if _, _, err := f.run(nil, "", "git", "merge"); err == nil {
		t.Error("merging main into itself should be refused")
	}
}

func TestGitAlias(t *testing.T) {
	f := newGitFixture(t)
	out, _, err := f.run(nil, "", "git", "alias", "save", "ac=save", "land=ship")
	if err != nil || !strings.Contains(out, "git ac  runs  zx git save") {
		t.Fatalf("alias: %v\n%s", err, out)
	}
	if got := f.git(t, "config", "--global", "alias.ac"); got != "!zx git save" {
		t.Errorf("alias.ac = %q", got)
	}
	if _, _, err := f.run(nil, "", "git", "alias", "tag"); err == nil || !strings.Contains(err.Error(), "built in") {
		t.Errorf("a git command should be protected, got %v", err)
	}
	if _, _, err := f.run(nil, "", "git", "alias", "x=nonsense"); err == nil {
		t.Error("an alias for something zx git does not have should be refused")
	}
	if out, _, _ := f.run(nil, "", "git", "alias", "--list"); !strings.Contains(out, "git ac") || !strings.Contains(out, "git save") {
		t.Errorf("list: %s", out)
	}
	if _, _, err := f.run(nil, "", "git", "alias", "--remove", "ac"); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := f.run(nil, "", "git", "alias", "--list"); strings.Contains(out, "git ac ") {
		t.Errorf("ac should be gone: %s", out)
	}
}

func TestGitOutsideARepo(t *testing.T) {
	f := newGitFixture(t)
	workDir = func() (string, error) { return t.TempDir(), nil }
	if _, _, err := f.run(nil, "", "git", "save"); err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Errorf("got %v", err)
	}
	if out, _, err := f.run(nil, "", "git"); err != nil || !strings.Contains(out, "zx git save") {
		t.Errorf("zx git with no arguments should list the commands: %v %q", err, out)
	}
}
