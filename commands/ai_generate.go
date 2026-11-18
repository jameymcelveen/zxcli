package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("__ask", runAsk) }

// generated is what the AI hands back: either questions, or a command.
type generated struct {
	Questions []string `json:"questions"`
	Name      string   `json:"name"`
	Summary   string   `json:"summary"`
	Language  string   `json:"language"`
	Script    string   `json:"script"`
}

const maxScriptBytes = 64 << 10

// buildPrompt describes the task, the machine and the folder to the AI. It
// sends file names, never file contents.
func buildPrompt(cfg core.Config, request, dir string, taken []string, qa []string, noMoreQuestions bool) string {
	var b strings.Builder
	b.WriteString("You write small command-line scripts for a tool called zx.\n\n")
	b.WriteString("The user wants a command that does this:\n" + request + "\n\n")
	langs := core.Languages()
	fmt.Fprintf(&b, "Machine: %s/%s. Script languages that can run here: %s. Use the first one that suits the job.\n",
		runtime.GOOS, runtime.GOARCH, strings.Join(langs, ", "))
	fmt.Fprintf(&b, "The command runs in the folder %q, a %s project.\n", filepath.Base(dir), DetectKind(dir))
	if entries, err := os.ReadDir(dir); err == nil {
		var names []string
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		if len(names) > 60 {
			names = append(names[:60], "...")
		}
		fmt.Fprintf(&b, "Files in that folder: %s\n", strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "Command names already in use: %s\n\n", strings.Join(taken, ", "))
	if len(qa) > 0 {
		b.WriteString("You already asked, and the user answered:\n" + strings.Join(qa, "\n") + "\n\n")
	}
	b.WriteString("Reply with ONE JSON object and nothing else: no prose, no code fence.\n\n")
	if noMoreQuestions {
		b.WriteString("Do not ask any questions. Make sensible choices and note them in comments in the script.\n\n")
	} else {
		b.WriteString("If a wrong guess would matter and only the user knows the answer, reply with up to 3 short questions:\n")
		b.WriteString(`{"questions": ["...", "..."]}` + "\n\nOtherwise reply ")
	}
	b.WriteString("with the command:\n")
	b.WriteString(`{"name": "...", "summary": "...", "language": "` + firstNonEmpty(strings.Join(langs[:min(1, len(langs))], ""), "sh") + `", "script": "..."}` + "\n")
	b.WriteString("- name: one or two lowercase words joined by a dash, not a name already in use\n")
	b.WriteString("- summary: one line under 70 characters, starting with a verb\n")
	b.WriteString("- language: one of the languages listed above\n")
	b.WriteString("- script: the complete file. Extra command-line arguments arrive as ordinary script arguments.\n")
	b.WriteString("- The script stops at the first error (set -eu in sh), prints each step as it does it, and checks that what it needs exists before changing anything.\n")
	b.WriteString("- It must not delete files, discard uncommitted work, force-push or rewrite git history unless the request asks for exactly that.\n")
	return b.String()
}

// extractJSON pulls the JSON object out of a reply that may be wrapped in a
// code fence or a sentence.
func extractJSON(reply string) (generated, error) {
	var g generated
	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return g, errors.New("no JSON object in the reply")
	}
	err := json.Unmarshal([]byte(reply[start:end+1]), &g)
	return g, err
}

var newNameLoose = newName // lowercase letters, digits and dashes

func (g *generated) validate() error {
	g.Name = strings.ToLower(strings.TrimSpace(g.Name))
	g.Language = strings.ToLower(strings.TrimSpace(g.Language))
	switch {
	case strings.TrimSpace(g.Script) == "":
		return errors.New("the reply had no script")
	case len(g.Script) > maxScriptBytes:
		return errors.New("the script is unreasonably long")
	case !newNameLoose.MatchString(g.Name):
		return fmt.Errorf("%q is not a usable command name", g.Name)
	case core.LanguageExt(g.Language) == "":
		return fmt.Errorf("unknown script language %q", g.Language)
	}
	if _, err := core.FileRunner("x" + core.LanguageExt(g.Language)); err != nil {
		return err
	}
	return nil
}

// takenNames lists every command name in use, sorted.
func takenNames(app *core.App) []string {
	seen := map[string]bool{}
	for _, it := range app.Names() {
		seen[it.Name] = true
	}
	for _, m := range app.Commands() {
		for _, a := range m.Aliases {
			seen[a] = true
		}
	}
	for a := range app.Config.Aliases {
		seen[a] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// generate asks the AI for a command, relaying its questions to the user.
func generate(c *core.Context, p *prompter, provider *aiProvider, request, dir string) (*generated, error) {
	taken := takenNames(c.App)
	var qa []string
	badReplies := 0
	for round := 0; round < 5; round++ {
		fmt.Fprintf(c.Stderr, "%s\n", c.App.Dim("asking "+provider.ID+"..."))
		reply, err := provider.Ask(c.App.Config, buildPrompt(c.App.Config, request, dir, taken, qa, len(qa) >= 6 || round >= 2))
		if err != nil {
			return nil, fmt.Errorf("%s did not answer: %w", provider.ID, err)
		}
		g, err := extractJSON(reply)
		if err == nil && len(g.Questions) == 0 {
			err = g.validate()
		}
		if err != nil {
			if badReplies++; badReplies >= 2 {
				return nil, fmt.Errorf("%s did not answer in the expected form (%v). Try again, or rephrase the request", provider.ID, err)
			}
			qa = append(qa, "(Your last reply could not be used: "+err.Error()+". Reply with the JSON object only.)")
			continue
		}
		if len(g.Questions) == 0 {
			return &g, nil
		}
		for _, q := range g.Questions {
			answer, ok := p.ask(c.App.Bold("? ") + q + "\n> ")
			if !ok {
				answer = "(no answer; use your best judgement)"
			}
			qa = append(qa, "Q: "+q+"\nA: "+answer)
		}
	}
	return nil, fmt.Errorf("%s kept asking questions. Try a more specific request", provider.ID)
}

// withHeader adds the summary and provenance comments zx reads back later.
func (g *generated) withHeader(providerID, request string) string {
	prefix := "#"
	if g.Language == "js" || g.Language == "ts" {
		prefix = "//"
	}
	request = strings.Join(strings.Fields(request), " ")
	header := prefix + " summary: " + g.Summary + "\n" +
		prefix + " made by zx ai (" + providerID + ") on " + time.Now().Format("2006-01-02") + ` from: "` + request + `"` + "\n"
	script := strings.ReplaceAll(g.Script, "\r\n", "\n")
	if strings.HasPrefix(script, "#!") {
		first, rest, _ := strings.Cut(script, "\n")
		script = first + "\n" + header + rest
	} else {
		if g.Language == "sh" {
			header = "#!/bin/sh\n" + header
		}
		script = header + script
	}
	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}
	return script
}

func showGenerated(c *core.Context, g *generated, script string) {
	lines := strings.Count(script, "\n")
	rule := c.App.Dim(strings.Repeat("-", 60))
	fmt.Fprintf(c.Stderr, "\n%s  %s  %s\n%s\n", c.App.Bold("zx "+g.Name), g.Summary,
		c.App.Dim(fmt.Sprintf("(%s, %d lines)", g.Language, lines)), rule)
	fmt.Fprint(c.Stderr, script)
	fmt.Fprintln(c.Stderr, rule)
}

// gitRoot walks up from dir to the folder that holds .git, or returns "".
func gitRoot(dir string) string {
	for {
		if fileExists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// saveGenerated writes the script into a commands folder: the project's
// (.zx/commands) or the user's. It returns the path it wrote.
func saveGenerated(c *core.Context, p *prompter, g *generated, script, dir, scope string) (string, error) {
	taken := map[string]bool{}
	for _, n := range takenNames(c.App) {
		taken[n] = true
	}
	for tries := 0; taken[g.Name] || !newNameLoose.MatchString(g.Name); tries++ {
		if tries >= 3 {
			return "", errors.New("no free name was given, so nothing was saved")
		}
		answer, ok := p.ask(fmt.Sprintf("%q is already a command. Name it what? ", g.Name))
		if !ok || answer == "" {
			return "", errors.New("no free name was given, so nothing was saved")
		}
		g.Name = strings.ToLower(answer)
	}

	folder := ""
	if scope != "user" {
		root := firstNonEmpty(c.App.Config.ProjectDir, gitRoot(dir))
		if root == "" && scope == "project" {
			root = dir
		}
		if root != "" {
			folder = filepath.Join(root, core.ProjectDirName, "commands")
		}
	}
	if folder == "" {
		if core.UserDir() == "" {
			return "", errors.New("can't work out where your commands live; set ZX_CONFIG")
		}
		folder = filepath.Join(core.UserDir(), "commands")
	}
	path := filepath.Join(folder, g.Name+core.LanguageExt(g.Language))
	if fileExists(path) {
		return "", fmt.Errorf("%s already exists, so nothing was saved", path)
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// aiNew implements zx new "<what the command should do>".
func aiNew(c *core.Context, request, scope string, yes bool) error {
	p := newPrompter(c)
	provider, err := ensureProvider(c, p)
	if err != nil {
		return err
	}
	dir, err := workDir()
	if err != nil {
		return err
	}
	g, err := generate(c, p, provider, request, dir)
	if err != nil {
		return err
	}
	script := g.withHeader(provider.ID, request)
	showGenerated(c, g, script)
	if !yes {
		answer, _ := p.ask(fmt.Sprintf("Save it as %s? [y/N] ", c.App.Bold("zx "+g.Name)))
		if !isYes(answer) {
			fmt.Fprintln(c.Stderr, "nothing was saved")
			return nil
		}
	}
	path, err := saveGenerated(c, p, g, script, dir, scope)
	if err != nil {
		return err
	}
	c.Printf("saved %s\nread it over, then run it with: zx %s\n", path, g.Name)
	return nil
}

// runAsk implements zx "<a sentence>": write a script for a one-off job,
// show it, and run it only if the user says so.
func runAsk(c *core.Context) error {
	if len(c.Args) == 1 && (c.Args[0] == "-h" || c.Args[0] == "--help") {
		_, err := c.Parse(c.Flags())
		return err
	}
	if len(c.Args) == 0 {
		return core.Usagef(`say what you want done: zx "bump the version and tag the repo"`)
	}
	request := strings.Join(c.Args, " ")
	p := newPrompter(c)
	provider, err := ensureProvider(c, p)
	if err != nil {
		return err
	}
	dir, err := workDir()
	if err != nil {
		return err
	}
	g, err := generate(c, p, provider, request, dir)
	if err != nil {
		return err
	}
	script := g.withHeader(provider.ID, request)
	showGenerated(c, g, script)

	ran := false
	if answer, _ := p.ask("Run it now? [y/N] "); isYes(answer) {
		ran = true
		if err := runOnce(c, g, script, dir); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(c.Stderr, "not run")
	}

	if c.App.Config.Setting("ai.offer-save", "") == "never" {
		return nil
	}
	verb := "Save it"
	if ran {
		verb = "It worked. Save it"
	}
	answer, _ := p.ask(fmt.Sprintf("%s as %s for next time? [y/N/never] ", verb, c.App.Bold("zx "+g.Name)))
	switch strings.ToLower(answer) {
	case "never":
		if err := core.SaveSetting(core.UserSettingsPath(), "ai.offer-save", "never"); err != nil {
			return err
		}
		fmt.Fprintf(c.Stderr, "ok, zx will not offer again. To undo that, remove \"ai.offer-save\" from %s\n", core.UserSettingsPath())
	case "y", "yes":
		path, err := saveGenerated(c, p, g, script, dir, "")
		if err != nil {
			return err
		}
		c.Printf("saved %s\nrun it with: zx %s\n", path, g.Name)
	}
	return nil
}

// runOnce runs a generated script from a temporary file in dir.
func runOnce(c *core.Context, g *generated, script, dir string) error {
	tmp, err := os.CreateTemp("", "zx-*"+core.LanguageExt(g.Language))
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(script); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	cmd, err := core.FileCommand(context.Background(), tmp.Name(), dir, nil)
	if err != nil {
		return err
	}
	if err := core.RunCommand(cmd, os.Stdin, c.Stdout, c.Stderr); err != nil {
		var exit *core.ExitError
		if errors.As(err, &exit) {
			fmt.Fprintf(c.Stderr, "the script stopped with exit status %d\n", exit.Code)
		}
		return err
	}
	return nil
}
