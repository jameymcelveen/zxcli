package commands

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("git", runGit) }

// gitSteps are the building blocks. A flow is an ordered list of them, and
// every step is also a command of its own: "zx git save", "zx git tag".
var gitSteps = []string{"save", "push", "sync", "test", "tag", "merge", "undo"}

// builtinFlows are flows that exist without any setup. A setting named
// git.flow.<name> adds one, or replaces one of these.
var builtinFlows = map[string]string{"ship": "save,push"}

// gitBuiltins are names "zx git alias" will not take over without --force,
// because they are real git commands.
var gitBuiltins = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`add am apply archive bisect blame branch bundle checkout cherry-pick clean clone commit config describe diff fetch format-patch gc grep help init log merge mv notes pull push range-diff rebase reflog remote reset restore revert rm shortlog show sparse-checkout stash status submodule switch tag worktree`) {
		gitBuiltins[n] = true
	}
}

func runGit(c *core.Context) error {
	if len(c.Args) == 0 || c.Args[0] == "help" {
		printGitHelp(c)
		return nil
	}
	if c.Args[0] == "-h" || c.Args[0] == "--help" {
		_, err := c.Parse(c.Flags()) // the standard help, then the list of commands
		printGitHelp(c)
		return err
	}
	sub := c.Args[0]
	c.Args = c.Args[1:]
	switch sub {
	case "alias":
		return gitAlias(c)
	}
	if fn, ok := gitStepFuncs[sub]; ok {
		return fn(c)
	}
	if steps, ok := gitFlow(c.App.Config, sub); ok {
		return runGitFlow(c, sub, steps)
	}
	return core.Usagef("%q is not a zx git command. Run: zx git", sub)
}

var gitStepFuncs map[string]func(*core.Context) error

func init() {
	gitStepFuncs = map[string]func(*core.Context) error{
		"save": gitSave, "push": gitPushCmd, "sync": gitSync, "test": gitTest,
		"tag": gitTag, "merge": gitMerge, "undo": gitUndo,
	}
}

func printGitHelp(c *core.Context) {
	b, d := c.App.Bold, c.App.Dim
	c.Println("zx git <command> [args]")
	c.Println()
	c.Println(b("Everyday"))
	c.Println("  save [message]   stage everything and commit. zx writes the message when you don't")
	c.Println("  push             push this branch (sets the upstream the first time)")
	c.Println("  sync             pull --rebase, then push")
	c.Println("  undo             take back the last commit and keep your changes")
	c.Println(b("Releasing"))
	c.Println("  test             run this project's tests")
	c.Println("  tag [version]    test, tag the next version (or patch, minor, major), push it")
	c.Println("  merge [branch]   merge a branch into main, push, and switch back")
	c.Println(b("Yours"))
	names := flowNames(c.App.Config)
	for _, n := range names {
		steps, _ := gitFlow(c.App.Config, n)
		c.Printf("  %-16s %s\n", n, d(strings.Join(steps, " then ")))
	}
	c.Println("  alias [names]    make git save, git ship... run these (git config alias)")
	c.Println()
	c.Println(d("Settings: git.flow.<name> (your own step lists), git.message (auto, ai, pattern),"))
	c.Println(d("git.test, git.main. Try: zx git save -n   (show what it would commit)"))
}

// ---- flows ----

func flowNames(cfg core.Config) []string {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n != "" && !seen[n] && !isGitStep(n) {
			seen[n] = true
			names = append(names, n)
		}
	}
	for n := range builtinFlows {
		add(n)
	}
	for k := range cfg.Settings {
		if strings.HasPrefix(k, "git.flow.") {
			add(strings.TrimPrefix(k, "git.flow."))
		}
	}
	sort.Strings(names)
	return names
}

func isGitStep(n string) bool {
	for _, s := range gitSteps {
		if s == n {
			return true
		}
	}
	return false
}

// gitFlow returns the steps of a flow: the git.flow.<name> setting, or a
// built-in one.
func gitFlow(cfg core.Config, name string) ([]string, bool) {
	if isGitStep(name) {
		return nil, false
	}
	line := cfg.Setting("git.flow."+name, builtinFlows[name])
	steps := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '+' })
	return steps, len(steps) > 0
}

func runGitFlow(c *core.Context, name string, steps []string) error {
	for _, s := range steps {
		if !isGitStep(s) || s == "undo" {
			return fmt.Errorf("git.flow.%s lists %q, which is not a step zx git can chain. Use: %s", name, s, strings.Join(gitSteps[:6], ", "))
		}
	}
	args := c.Args
	for _, s := range steps {
		fmt.Fprintln(c.Stderr, c.App.Bold("zx git "+s))
		if s == "save" {
			c.Args = args // a message and save's flags belong to the save step
		} else {
			c.Args = nil
		}
		if err := gitStepFuncs[s](c); err != nil {
			return err
		}
	}
	return nil
}

// ---- running git ----

type gitRepo struct {
	c    *core.Context
	root string
	env  []string
}

func openRepo(c *core.Context) (*gitRepo, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("git is not installed")
	}
	dir, err := workDir()
	if err != nil {
		return nil, err
	}
	g := &gitRepo{c: c, root: dir}
	out, err := g.out("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("this folder is not inside a git repository")
	}
	g.root = strings.TrimSpace(out)
	return g, nil
}

func (g *gitRepo) cmd(args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", g.root, "-c", "core.quotepath=false"}, args...)...)
	if g.env != nil {
		cmd.Env = append(os.Environ(), g.env...)
	}
	return cmd
}

// out runs git and returns what it printed.
func (g *gitRepo) out(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := g.cmd(args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

func (g *gitRepo) ok(args ...string) bool { return g.cmd(args...).Run() == nil }

// do runs git with its output shown. echo prints the command first, which
// zx does for anything that touches the network or moves branches.
func (g *gitRepo) do(echo bool, args ...string) error {
	if echo {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = quoteArg(a)
		}
		fmt.Fprintln(g.c.Stderr, g.c.App.Dim("$ git "+strings.Join(parts, " ")))
	}
	cmd := g.cmd(args...)
	cmd.Stdout, cmd.Stderr = g.c.Stdout, g.c.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s failed", args[0])
	}
	return nil
}

func quoteArg(a string) string {
	if a == "" || strings.ContainsAny(a, " \t\"'$&|;<>()") {
		return strconv.Quote(a)
	}
	return a
}

func (g *gitRepo) branch() string {
	out, err := g.out("symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (g *gitRepo) upstream() string {
	out, err := g.out("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (g *gitRepo) remote() string {
	out, _ := g.out("remote")
	var first string
	for _, r := range strings.Fields(out) {
		if r == "origin" {
			return r
		}
		if first == "" {
			first = r
		}
	}
	return first
}

func (g *gitRepo) clean() bool {
	out, err := g.out("status", "--porcelain")
	return err == nil && strings.TrimSpace(out) == ""
}

func (g *gitRepo) mainBranch() string {
	if m := g.c.App.Config.Setting("git.main", ""); m != "" {
		return m
	}
	if out, err := g.out("symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); b != "" {
			return b
		}
	}
	for _, b := range []string{"main", "master"} {
		if g.ok("show-ref", "--verify", "--quiet", "refs/heads/"+b) {
			return b
		}
	}
	return "main"
}

// ---- save ----

type change struct {
	Path    string
	Status  string // A, M or D
	Added   int
	Deleted int
	Binary  bool
}

func verbOf(status string) string {
	switch status {
	case "A":
		return "Add"
	case "D":
		return "Delete"
	}
	return "Update"
}

func (g *gitRepo) staged() ([]change, error) {
	ns, err := g.out("diff", "--cached", "--no-renames", "--name-status")
	if err != nil {
		return nil, err
	}
	nu, err := g.out("diff", "--cached", "--no-renames", "--numstat")
	if err != nil {
		return nil, err
	}
	type counts struct {
		a, d   int
		binary bool
	}
	numbers := map[string]counts{}
	for _, line := range strings.Split(nu, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 {
			continue
		}
		if f[0] == "-" {
			numbers[f[2]] = counts{binary: true}
			continue
		}
		a, _ := strconv.Atoi(f[0])
		d, _ := strconv.Atoi(f[1])
		numbers[f[2]] = counts{a: a, d: d}
	}
	var files []change
	for _, line := range strings.Split(ns, "\n") {
		f := strings.SplitN(line, "\t", 2)
		if len(f) != 2 || f[0] == "" {
			continue
		}
		n := numbers[f[1]]
		files = append(files, change{Path: f[1], Status: f[0][:1], Added: n.a, Deleted: n.d, Binary: n.binary})
	}
	return files, nil
}

func totals(files []change) (added, deleted int) {
	for _, f := range files {
		added += f.Added
		deleted += f.Deleted
	}
	return
}

// patternMessage writes a commit message from nothing but the list of
// changes. confident is false when the change is too mixed for a pattern to
// say anything useful, which is when zx asks the AI instead.
func patternMessage(files []change) (subject, body string, confident bool) {
	n := len(files)
	if n == 0 {
		return "", "", false
	}
	added, deleted := totals(files)
	sum := fmt.Sprintf("+%d -%d", added, deleted)
	if n == 1 {
		f := files[0]
		if f.Binary {
			sum = "binary file"
		}
		return verbOf(f.Status) + " " + f.Path, sum, true
	}
	if n <= 3 {
		if s := listSubject(files); len(s) <= 72 {
			var lines []string
			for _, f := range files {
				lines = append(lines, fileLine(f))
			}
			return s, strings.Join(lines, "\n"), true
		}
	}
	dir := commonDir(files)
	subject = manySubject(files, dir)
	lines := []string{fmt.Sprintf("%s changed, %s", plural(n, "1 file", fmt.Sprintf("%d files", n)), sum), ""}
	for i, f := range files {
		if i == 15 {
			lines = append(lines, fmt.Sprintf("... and %d more", n-15))
			break
		}
		lines = append(lines, f.Status+" "+f.Path)
	}
	return subject, strings.Join(lines, "\n"), dir != "" || n <= 3
}

func fileLine(f change) string {
	if f.Binary {
		return f.Path + "  binary"
	}
	return fmt.Sprintf("%s  +%d -%d", f.Path, f.Added, f.Deleted)
}

func listSubject(files []change) string {
	var parts []string
	for _, v := range []string{"Add", "Update", "Delete"} {
		var paths []string
		for _, f := range files {
			if verbOf(f.Status) == v {
				paths = append(paths, f.Path)
			}
		}
		if len(paths) == 0 {
			continue
		}
		word := v
		if len(parts) > 0 {
			word = strings.ToLower(v)
		}
		parts = append(parts, word+" "+strings.Join(paths, ", "))
	}
	return strings.Join(parts, ", ")
}

func manySubject(files []change, dir string) string {
	verbs := map[string]bool{}
	for _, f := range files {
		verbs[verbOf(f.Status)] = true
	}
	verb := "Change"
	if len(verbs) == 1 {
		for v := range verbs {
			verb = v
		}
	}
	s := fmt.Sprintf("%s %d files", verb, len(files))
	if dir != "" {
		s += " in " + dir
	}
	return s
}

// commonDir is the folder every changed file lives under, or "".
func commonDir(files []change) string {
	var common []string
	for i, f := range files {
		parts := strings.Split(path.Dir(f.Path), "/")
		if path.Dir(f.Path) == "." {
			return ""
		}
		if i == 0 {
			common = parts
			continue
		}
		k := 0
		for k < len(common) && k < len(parts) && common[k] == parts[k] {
			k++
		}
		common = common[:k]
	}
	if len(common) == 0 {
		return ""
	}
	return strings.Join(common, "/") + "/"
}

// riskyFiles are staged files that usually hold secrets.
func riskyFiles(files []change) []string {
	var risky []string
	for _, f := range files {
		if f.Status == "D" {
			continue
		}
		base := strings.ToLower(path.Base(f.Path))
		bad := false
		switch {
		case base == ".env" || strings.HasPrefix(base, ".env."):
			ext := path.Ext(base)
			bad = ext != ".example" && ext != ".sample" && ext != ".template" && ext != ".dist"
		case base == ".netrc" || base == "credentials.json":
			bad = true
		case strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519"):
			bad = !strings.HasSuffix(base, ".pub")
		default:
			switch path.Ext(base) {
			case ".pem", ".key", ".p12", ".pfx":
				bad = true
			}
		}
		if bad {
			risky = append(risky, f.Path)
		}
	}
	return risky
}

// cleanCommitReply turns whatever a model sent back into a subject and body.
func cleanCommitReply(reply string) (subject, body string, err error) {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(reply, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			continue
		}
		lines = append(lines, strings.TrimRight(l, " \t"))
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return "", "", errors.New("the AI sent back an empty message")
	}
	first, rest, _ := strings.Cut(text, "\n")
	subject = strings.Trim(strings.TrimSpace(first), "\"'`")
	subject = strings.TrimSuffix(subject, ".")
	body = strings.TrimSpace(rest)
	if len(subject) > 72 {
		cut := strings.LastIndex(subject[:72], " ")
		if cut < 30 {
			cut = 72
		}
		body = strings.TrimSpace(subject[cut:] + "\n" + body)
		subject = strings.TrimSpace(subject[:cut])
	}
	if subject == "" {
		return "", "", errors.New("the AI sent back an empty message")
	}
	return subject, body, nil
}

func (g *gitRepo) aiMessage(provider *aiProvider, files []change) (string, string, error) {
	diff, _ := g.out("diff", "--cached", "--no-renames", "--no-color", "-U2")
	if len(diff) > 12000 {
		cut := strings.LastIndex(diff[:12000], "\n")
		if cut < 0 {
			cut = 12000
		}
		diff = diff[:cut] + "\n[diff cut short]"
	}
	stat, _ := g.out("diff", "--cached", "--no-renames", "--stat")
	recent, _ := g.out("log", "-n", "8", "--format=%s")
	var sb strings.Builder
	sb.WriteString("Write a git commit message for the staged changes below.\n")
	sb.WriteString("Rules: the first line is a summary in the imperative mood (\"Add\", \"Fix\", \"Rename\"), at most 72 characters, with no trailing period and no quotes. ")
	sb.WriteString("You may add a blank line and then a short body of one to four lines about what changed and why. ")
	sb.WriteString("Reply with the message only: no code fences, no commentary.\n")
	if strings.TrimSpace(recent) != "" {
		sb.WriteString("\nRecent commit subjects in this repository, for style:\n" + strings.TrimSpace(recent) + "\n")
	}
	sb.WriteString("\nFiles:\n" + strings.TrimSpace(stat) + "\n\nDiff (secret-looking values hidden):\n" + redact(diff) + "\n")
	reply, err := provider.Ask(g.c.App.Config, sb.String())
	if err != nil {
		return "", "", askError(provider.ID, err)
	}
	return cleanCommitReply(reply)
}

type saveOpts struct {
	message string
	ai      bool
	pattern bool
}

// chooseMessage decides who writes the commit message and returns it with a
// short note on where it came from.
func (g *gitRepo) chooseMessage(p *prompter, files []change, o saveOpts) (subject, body, source string, err error) {
	if strings.TrimSpace(o.message) != "" {
		s, b, _ := strings.Cut(strings.TrimSpace(o.message), "\n")
		return strings.TrimSpace(s), strings.TrimSpace(b), "you", nil
	}
	cfg := g.c.App.Config
	mode := cfg.Setting("git.message", "auto")
	if o.ai {
		mode = "ai"
	}
	if o.pattern {
		mode = "pattern"
	}
	if mode != "auto" && mode != "ai" && mode != "pattern" {
		return "", "", "", fmt.Errorf("git.message is %q: use auto, ai or pattern", mode)
	}
	ps, pb, confident := patternMessage(files)
	if mode == "pattern" || (mode == "auto" && confident) {
		return ps, pb, "pattern", nil
	}
	var provider *aiProvider
	var perr error
	switch {
	case mode == "auto" && cfg.Setting("ai.provider", "") == "":
		perr = errors.New("no AI chosen yet. Run: zx ai")
	default:
		provider, perr = ensureProvider(g.c, p)
	}
	if perr == nil {
		fmt.Fprintln(g.c.Stderr, g.c.App.Dim("asking "+provider.ID+" for a message..."))
		s, b, aerr := g.aiMessage(provider, files)
		if aerr == nil {
			return s, b, "AI: " + provider.ID, nil
		}
		perr = aerr
	}
	fmt.Fprintf(g.c.Stderr, "zx: could not use AI for the message (%v). Using a plain one.\n", perr)
	return ps, pb, "pattern", nil
}

// tempIndex points git at a copy of the index, so a dry run can stage
// everything without touching the real one.
func (g *gitRepo) tempIndex() (func(), error) {
	idx, err := g.out("rev-parse", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	idx = strings.TrimSpace(idx)
	if !filepath.IsAbs(idx) {
		idx = filepath.Join(g.root, idx)
	}
	tmp, err := os.CreateTemp("", "zx-index-*")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	if data, err := os.ReadFile(idx); err == nil {
		if err := os.WriteFile(tmp.Name(), data, 0o600); err != nil {
			return nil, err
		}
	} else {
		os.Remove(tmp.Name()) // let git make a fresh one
	}
	g.env = append(g.env, "GIT_INDEX_FILE="+tmp.Name())
	return func() { os.Remove(tmp.Name()) }, nil
}

func gitSave(c *core.Context) error {
	fs := c.Flags()
	m := fs.String("m", "", "commit message (zx writes one when you leave this out)")
	long := fs.String("message", "", "same as -m")
	dry := fs.Bool("n", false, "show what would be committed and the message, change nothing")
	fs.BoolVar(dry, "dry-run", false, "same as -n")
	staged := fs.Bool("staged", false, "commit only what is already staged")
	ai := fs.Bool("ai", false, "have the AI write the message")
	pattern := fs.Bool("pattern", false, "write the message from the file list, never ask the AI")
	push := fs.Bool("push", false, "push after committing")
	force := fs.Bool("force", false, "commit even files that look like secrets")
	noVerify := fs.Bool("no-verify", false, "skip git hooks")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	g, err := openRepo(c)
	if err != nil {
		return err
	}
	message := *m
	if *long != "" {
		message = *long
	}
	if message == "" && len(pos) > 0 {
		message = strings.Join(pos, " ")
	}

	if !*staged {
		if *dry {
			cleanup, err := g.tempIndex()
			if err != nil {
				return err
			}
			defer cleanup()
		}
		if err := g.do(false, "add", "-A"); err != nil {
			return err
		}
	}
	files, err := g.staged()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		c.Println("nothing to commit, working tree clean")
		return nil
	}
	if risky := riskyFiles(files); len(risky) > 0 && !*force {
		if !*dry {
			g.do(false, append([]string{"reset", "-q", "--"}, risky...)...)
		}
		return fmt.Errorf("these look like secrets, so nothing was committed: %s\nAdd them to .gitignore, or run again with --force", strings.Join(risky, ", "))
	}

	p := newPrompter(c)
	subject, body, source, err := g.chooseMessage(p, files, saveOpts{message: message, ai: *ai, pattern: *pattern})
	if err != nil {
		return err
	}
	added, deleted := totals(files)
	c.Printf("%s, +%d -%d\n", plural(len(files), "1 file", fmt.Sprintf("%d files", len(files))), added, deleted)
	if len(files) <= 8 {
		for _, f := range files {
			c.Println(c.App.Dim("  " + f.Status + " " + fileLine(f)))
		}
	}
	if *dry {
		c.Printf("%s %s\n", c.App.Bold("would commit:"), subject)
		if body != "" {
			c.Println(c.App.Dim(indent(body)))
		}
		c.Println(c.App.Dim("message from " + source))
		return nil
	}
	args := []string{"commit", "-q", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	if *noVerify {
		args = append(args, "--no-verify")
	}
	if err := g.do(false, args...); err != nil {
		return err
	}
	short, _ := g.out("rev-parse", "--short", "HEAD")
	c.Printf("%s %s %s\n", c.App.Bold("committed"), strings.TrimSpace(short), subject)
	c.Println(c.App.Dim("message from " + source))
	if *push {
		return pushBranch(g)
	}
	return nil
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// ---- push and sync ----

func pushBranch(g *gitRepo) error {
	branch := g.branch()
	if branch == "" {
		return errors.New("HEAD is detached, so there is no branch to push")
	}
	if g.upstream() != "" {
		return g.do(true, "push")
	}
	remote := g.remote()
	if remote == "" {
		return errors.New("there is no remote to push to. Add one: git remote add origin <url>")
	}
	return g.do(true, "push", "-u", remote, branch)
}

func gitPushCmd(c *core.Context) error {
	if _, err := c.Parse(c.Flags()); err != nil {
		return err
	}
	g, err := openRepo(c)
	if err != nil {
		return err
	}
	return pushBranch(g)
}

func gitSync(c *core.Context) error {
	if _, err := c.Parse(c.Flags()); err != nil {
		return err
	}
	g, err := openRepo(c)
	if err != nil {
		return err
	}
	if g.upstream() != "" {
		if err := g.do(true, "pull", "--rebase", "--autostash"); err != nil {
			return fmt.Errorf("%v. Fix the conflicts, then run: git rebase --continue", err)
		}
	}
	return pushBranch(g)
}

// ---- test ----

// testCommand is the project's test command: the git.test setting, or a
// guess from the files in the folder.
func testCommand(cfg core.Config, root string) string {
	if t := cfg.Setting("git.test", ""); t != "" {
		return t
	}
	has := func(name string) bool { return fileExists(filepath.Join(root, name)) }
	if data, err := os.ReadFile(filepath.Join(root, "Makefile")); err == nil && regexp.MustCompile(`(?m)^test\s*:`).Match(data) {
		return "make test"
	}
	switch {
	case has("go.mod"):
		return "go test ./..."
	case has("package.json"):
		data, _ := os.ReadFile(filepath.Join(root, "package.json"))
		if regexp.MustCompile(`"test"\s*:`).Match(data) && !bytes.Contains(data, []byte("no test specified")) {
			return "npm test"
		}
	}
	if m, _ := filepath.Glob(filepath.Join(root, "*.sln")); len(m) > 0 {
		return "dotnet test"
	}
	if m, _ := filepath.Glob(filepath.Join(root, "*.csproj")); len(m) > 0 {
		return "dotnet test"
	}
	switch {
	case has("Cargo.toml"):
		return "cargo test"
	case has("pom.xml"):
		return "mvn test"
	case has("pytest.ini") || has("pyproject.toml"):
		return "python -m pytest"
	}
	return ""
}

func runTests(g *gitRepo, line string) error {
	fmt.Fprintln(g.c.Stderr, g.c.App.Dim("$ "+line))
	cmd := core.ShellCommand(line, g.root)
	cmd.Stdout, cmd.Stderr = g.c.Stdout, g.c.Stderr
	if err := cmd.Run(); err != nil {
		return errors.New("the tests failed")
	}
	return nil
}

func gitTest(c *core.Context) error {
	if _, err := c.Parse(c.Flags()); err != nil {
		return err
	}
	g, err := openRepo(c)
	if err != nil {
		return err
	}
	line := testCommand(c.App.Config, g.root)
	if line == "" {
		return errors.New("zx could not tell how this project runs its tests. Set it: \"settings\": { \"git.test\": \"make test\" } in .zx.json")
	}
	return runTests(g, line)
}

// ---- tag ----

var semverTag = regexp.MustCompile(`^(v?)(\d+)\.(\d+)\.(\d+)$`)

// nextTag works out the tag to create from the existing tags and what the
// person asked for: nothing or "patch", "minor", "major", or a version.
func nextTag(existing []string, arg string) (string, error) {
	prefix, top := "v", [3]int{-1, 0, 0}
	found := false
	for _, t := range existing {
		m := semverTag.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		v := [3]int{}
		for i := range v {
			v[i], _ = strconv.Atoi(m[i+2])
		}
		if !found || v[0] > top[0] || (v[0] == top[0] && (v[1] > top[1] || (v[1] == top[1] && v[2] > top[2]))) {
			top, prefix, found = v, m[1], true
		}
	}
	switch arg {
	case "", "patch", "minor", "major":
		if !found {
			if arg == "major" {
				return prefix + "1.0.0", nil
			}
			return prefix + "0.1.0", nil
		}
		switch arg {
		case "major":
			top = [3]int{top[0] + 1, 0, 0}
		case "minor":
			top = [3]int{top[0], top[1] + 1, 0}
		default:
			top[2]++
		}
		return fmt.Sprintf("%s%d.%d.%d", prefix, top[0], top[1], top[2]), nil
	}
	if m := semverTag.FindStringSubmatch(arg); m != nil {
		return prefix + strings.TrimPrefix(arg, "v"), nil
	}
	return "", fmt.Errorf("%q is not a version. Use 1.2.3, or patch, minor or major", arg)
}

func gitTag(c *core.Context) error {
	fs := c.Flags()
	m := fs.String("m", "", "tag message (default: the tag name)")
	noTest := fs.Bool("no-test", false, "do not run the tests first")
	noPush := fs.Bool("no-push", false, "create the tag but do not push")
	dry := fs.Bool("n", false, "show what would happen, change nothing")
	fs.BoolVar(dry, "dry-run", false, "same as -n")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	g, err := openRepo(c)
	if err != nil {
		return err
	}
	arg := ""
	if len(pos) > 0 {
		arg = pos[0]
	}
	g.cmd("fetch", "--tags", "-q").Run() // best effort, so a tag made elsewhere is not reused
	out, _ := g.out("tag", "--list")
	name := ""
	if name, err = nextTag(strings.Fields(out), arg); err != nil {
		return err
	}
	if g.ok("rev-parse", "-q", "--verify", "refs/tags/"+name) {
		return fmt.Errorf("the tag %s already exists", name)
	}
	if !g.clean() {
		return errors.New("there are uncommitted changes. Commit them first: zx git save")
	}
	line := ""
	if !*noTest {
		line = testCommand(c.App.Config, g.root)
	}
	if *dry {
		if line != "" {
			c.Println("would run the tests:", line)
		}
		c.Println("would tag", name, "at", strings.TrimSpace(mustOut(g, "rev-parse", "--short", "HEAD")))
		if !*noPush {
			c.Println("would push the branch, then", name)
		}
		return nil
	}
	if line != "" {
		if err := runTests(g, line); err != nil {
			return errors.New("the tests failed, so nothing was tagged")
		}
	} else if !*noTest {
		fmt.Fprintln(c.Stderr, c.App.Dim("no test command found (set git.test), tagging without tests"))
	}
	msg := *m
	if msg == "" {
		msg = name
	}
	if err := g.do(true, "tag", "-a", name, "-m", msg); err != nil {
		return err
	}
	if *noPush {
		c.Println("tagged", name)
		return nil
	}
	if err := pushBranch(g); err != nil {
		return err
	}
	remote := g.remote()
	if remote == "" {
		return errors.New("there is no remote to push the tag to")
	}
	if err := g.do(true, "push", remote, name); err != nil {
		return err
	}
	c.Println("tagged and pushed", name)
	return nil
}

func mustOut(g *gitRepo, args ...string) string {
	out, _ := g.out(args...)
	return out
}

// ---- merge ----

func gitMerge(c *core.Context) error {
	fs := c.Flags()
	squash := fs.Bool("squash", false, "squash the branch into one commit")
	noFF := fs.Bool("no-ff", false, "always make a merge commit")
	noPush := fs.Bool("no-push", false, "do not push main afterwards")
	del := fs.Bool("delete", false, "delete the branch after merging")
	dry := fs.Bool("n", false, "show the steps, change nothing")
	fs.BoolVar(dry, "dry-run", false, "same as -n")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	g, err := openRepo(c)
	if err != nil {
		return err
	}
	main := g.mainBranch()
	source := ""
	if len(pos) > 0 {
		source = pos[0]
	} else {
		source = g.branch()
	}
	switch {
	case source == "":
		return errors.New("HEAD is detached. Name the branch to merge: zx git merge <branch>")
	case source == main:
		return fmt.Errorf("you are on %s already. Name the branch to merge: zx git merge <branch>", main)
	case !g.ok("rev-parse", "-q", "--verify", "refs/heads/"+source):
		return fmt.Errorf("there is no branch called %s", source)
	}
	if !g.clean() {
		return errors.New("there are uncommitted changes. Commit them first: zx git save")
	}
	mergeArgs := []string{"merge"}
	switch {
	case *squash:
		mergeArgs = append(mergeArgs, "--squash")
	case *noFF:
		mergeArgs = append(mergeArgs, "--no-ff", "--no-edit")
	default:
		mergeArgs = append(mergeArgs, "--no-edit")
	}
	mergeArgs = append(mergeArgs, source)
	if *dry {
		c.Printf("would: checkout %s, pull, git %s, commit if squashed, push, delete %s: %v\n", main, strings.Join(mergeArgs, " "), source, *del)
		return nil
	}
	if g.branch() != main {
		if err := g.do(true, "checkout", main); err != nil {
			return err
		}
	}
	if g.upstream() != "" {
		if err := g.do(true, "pull", "--ff-only"); err != nil {
			return fmt.Errorf("%v. %s has diverged from its remote: sort that out first", err, main)
		}
	}
	if err := g.do(true, mergeArgs...); err != nil {
		return fmt.Errorf("the merge stopped. Fix the conflicts and commit, or undo it with: git merge --abort")
	}
	if *squash {
		if err := g.do(true, "commit", "-q", "-m", "Merge "+source+" (squashed)"); err != nil {
			return err
		}
	}
	if !*noPush {
		if err := pushBranch(g); err != nil {
			return err
		}
	}
	if *del {
		flag := "-d"
		if *squash {
			flag = "-D" // a squash never looks merged to git
		}
		if err := g.do(true, "branch", flag, source); err != nil {
			return err
		}
	}
	c.Printf("merged %s into %s\n", source, main)
	return nil
}

// ---- undo ----

func gitUndo(c *core.Context) error {
	fs := c.Flags()
	force := fs.Bool("force", false, "undo a commit that is already pushed")
	if _, err := c.Parse(fs); err != nil {
		return err
	}
	g, err := openRepo(c)
	if err != nil {
		return err
	}
	if !g.ok("rev-parse", "-q", "--verify", "HEAD~1") {
		return errors.New("there is no earlier commit to go back to")
	}
	subject := strings.TrimSpace(mustOut(g, "log", "-1", "--format=%s"))
	if g.upstream() != "" && !*force {
		if out, err := g.out("rev-list", "--count", "@{u}..HEAD"); err == nil && strings.TrimSpace(out) == "0" {
			return errors.New("that commit is already pushed. Undoing it would need a force push. Use --force to undo it here anyway")
		}
	}
	if err := g.do(false, "reset", "--soft", "HEAD~1"); err != nil {
		return err
	}
	c.Printf("undid %q. Your changes are still staged.\n", subject)
	return nil
}

// ---- alias ----

func gitAlias(c *core.Context) error {
	fs := c.Flags()
	list := fs.Bool("list", false, "show the git aliases zx made")
	remove := fs.Bool("remove", false, "remove the named aliases")
	local := fs.Bool("local", false, "set them in this repository only, not for every repository")
	force := fs.Bool("force", false, "replace an alias or git command that already exists")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed")
	}
	scope := "--global"
	if *local {
		scope = "--local"
	}
	gitCfg := func(args ...string) (string, error) {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command("git", append([]string{"config", scope}, args...)...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err != nil && stderr.Len() > 0 {
			err = errors.New(firstLine(stderr.String()))
		}
		return strings.TrimSpace(stdout.String()), err
	}
	if *list {
		out, _ := gitCfg("--get-regexp", `^alias\.`)
		n := 0
		for _, line := range strings.Split(out, "\n") {
			k, v, _ := strings.Cut(line, " ")
			if strings.HasPrefix(v, "!zx git ") {
				c.Printf("git %-10s %s\n", strings.TrimPrefix(k, "alias."), strings.TrimPrefix(v, "!"))
				n++
			}
		}
		if n == 0 {
			c.Println("no zx git aliases yet. Make them with: zx git alias")
		}
		return nil
	}
	if len(pos) == 0 && !*remove {
		pos = []string{"save", "ship", "sync", "undo"}
	}
	if len(pos) == 0 {
		return errors.New("name the aliases to remove")
	}
	for _, spec := range pos {
		name, target, hasTarget := strings.Cut(spec, "=")
		if !hasTarget {
			target = name
		}
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`).MatchString(name) {
			return fmt.Errorf("%q is not a usable alias name", name)
		}
		current, _ := gitCfg("--get", "alias."+name)
		if *remove {
			if !strings.HasPrefix(current, "!zx git ") && !*force {
				fmt.Fprintf(c.Stderr, "zx: git %s is not a zx alias, leaving it alone\n", name)
				continue
			}
			if _, err := gitCfg("--unset", "alias."+name); err != nil {
				return err
			}
			c.Println("removed git", name)
			continue
		}
		if _, ok := gitFlow(c.App.Config, target); !ok && !isGitStep(target) {
			return fmt.Errorf("zx git has no %q. Run: zx git", target)
		}
		if gitBuiltins[name] && !*force {
			return fmt.Errorf("git %s is built in to git. Pick another name, such as: zx git alias release=%s", name, target)
		}
		value := "!zx git " + target
		if current != "" && current != value && !*force {
			fmt.Fprintf(c.Stderr, "zx: git %s is already an alias for %q, leaving it. Use --force to replace it\n", name, current)
			continue
		}
		if _, err := gitCfg("alias."+name, value); err != nil {
			return err
		}
		c.Printf("git %s  runs  zx git %s\n", name, target)
	}
	return nil
}
