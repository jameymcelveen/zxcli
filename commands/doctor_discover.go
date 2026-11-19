package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

// discoverStacks looks at dir and says what it found. Recognising the common
// stacks needs no AI. When a person is at the keyboard, zx can then ask an AI
// what else the project needs, after showing exactly what it would send.
func discoverStacks(c *core.Context, p *prompter, dir string, noAI, quiet bool) ([]foundStack, []core.DoctorCheck) {
	found := findStacks(dir)
	say := func(format string, a ...any) {
		if !quiet {
			c.Printf(format, a...)
		}
	}
	say("Looked at %s\n", dir)
	if len(found) == 0 {
		say("Nothing familiar found here.\n")
	} else {
		say("Found:\n")
		for _, f := range found {
			name := f.stack.Name
			if f.rel != "" {
				name += " (" + f.rel + "/)"
			}
			say("  %s  %s\n", padRight(name, 18), c.App.Dim(f.why))
		}
	}
	say("\n")
	if noAI || quiet || !c.App.StdinTTY {
		return found, nil
	}
	return found, askAIForMore(c, p, dir, found)
}

// A suggestion is one extra tool the AI thinks the project needs.
type suggestion struct {
	Tool    string            `json:"tool"`
	Min     string            `json:"min"`
	Why     string            `json:"why"`
	Install map[string]string `json:"install"`
}

type contextFile struct {
	name, text string
}

var contextNames = []string{
	"README.md", "README", "CONTRIBUTING.md", "Makefile", "justfile", "Dockerfile",
	"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml",
	".env.example", "package.json", "composer.json", "Gemfile", ".tool-versions",
}

const (
	contextPerFile = 4000
	contextTotal   = 20000
)

var secretLine = regexp.MustCompile(`(?i)((?:secret|token|passw(?:or)?d|api[_-]?key|private[_-]?key|credential)[\w-]*\s*[:=]\s*)\S+`)

// redact hides anything that looks like a secret's value.
func redact(text string) string {
	return secretLine.ReplaceAllString(text, "${1}<redacted>")
}

// envNamesOnly keeps the variable names from an .env.example, not the values.
func envNamesOnly(text string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, _, ok := strings.Cut(line, "="); ok {
			out = append(out, strings.TrimSpace(k)+"=")
		}
	}
	return strings.Join(out, "\n")
}

// gatherContext reads the few small files that say what a project needs.
// Values in .env.example are dropped, secret-looking lines are redacted, and
// each file and the total are capped. .env itself is never read.
func gatherContext(dir string) []contextFile {
	var names []string
	names = append(names, contextNames...)
	if flows, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.y*ml")); len(flows) > 0 {
		sort.Strings(flows)
		for i, f := range flows {
			if i < 3 {
				rel, _ := filepath.Rel(dir, f)
				names = append(names, filepath.ToSlash(rel))
			}
		}
	}
	var out []contextFile
	total := 0
	for _, name := range names {
		text := readText(dir, filepath.FromSlash(name))
		if strings.TrimSpace(text) == "" {
			continue
		}
		if name == ".env.example" {
			text = envNamesOnly(text)
		}
		if len(text) > contextPerFile {
			text = text[:contextPerFile] + "\n[...cut]"
		}
		text = redact(text)
		if total+len(text) > contextTotal {
			break
		}
		total += len(text)
		out = append(out, contextFile{name, text})
	}
	return out
}

func discoverPrompt(dir string, found []foundStack, files []contextFile) string {
	var b strings.Builder
	b.WriteString("You help set up a developer's machine for a software project, for a tool called zx.\n\n")
	fmt.Fprintf(&b, "Machine: %s/%s. The project folder is %q.\n", runtime.GOOS, runtime.GOARCH, filepath.Base(dir))
	var checked []string
	for _, f := range found {
		for _, ck := range f.checks {
			if ck.Tool != "" {
				checked = append(checked, ck.Tool)
			}
		}
	}
	fmt.Fprintf(&b, "zx already checks these programs, so do not suggest them: %s\n", strings.Join(append(checked, strings.Split(DefaultDoctorTools, ", ")...), ", "))
	if entries, err := os.ReadDir(dir); err == nil {
		var names []string
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() {
				n += "/"
			}
			names = append(names, n)
		}
		if len(names) > 60 {
			names = append(names[:60], "...")
		}
		fmt.Fprintf(&b, "Files in the folder: %s\n", strings.Join(names, ", "))
	}
	b.WriteString("\nSome of the project's files follow, trimmed, with secrets removed:\n")
	for _, f := range files {
		fmt.Fprintf(&b, "\n=== %s ===\n%s\n", f.name, f.text)
	}
	b.WriteString(`
Suggest the other command-line programs someone must have installed to work on this project, judging from the files above: for example a database client, redis, ffmpeg, protoc, terraform, kubectl, a package manager. Only programs that really are run from a terminal, and only ones the files give evidence for.
Reply with ONE JSON object and nothing else: no prose, no code fence.
{"tools": [{"tool": "psql", "min": "15", "why": "docker-compose.yml runs postgres", "install": {"darwin": "brew install libpq", "linux": "sudo apt-get install -y postgresql-client", "windows": "winget install -e --id PostgreSQL.PostgreSQL.16"}}]}
- tool: the program name as typed in a terminal
- min: the oldest major version that works, or "" when unknown
- why: under 80 characters, naming the file that shows the need
- install: one command per system, only when you are sure of it; omit what you are not sure of
Give at most 8. If nothing more is needed, reply {"tools": []}.
`)
	return b.String()
}

var toolName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,39}$`)

// parseSuggestions turns the AI's reply into checks, dropping anything odd.
func parseSuggestions(reply, dir string, skip map[string]bool) ([]core.DoctorCheck, error) {
	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return nil, errors.New("no JSON object in the reply")
	}
	var s struct {
		Tools []suggestion `json:"tools"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &s); err != nil {
		return nil, err
	}
	var out []core.DoctorCheck
	seen := map[string]bool{}
	for _, t := range s.Tools {
		t.Tool = strings.TrimSpace(t.Tool)
		if !toolName.MatchString(t.Tool) || skip[t.Tool] || seen[t.Tool] || len(out) >= 8 {
			continue
		}
		seen[t.Tool] = true
		ck := core.DoctorCheck{Tool: t.Tool, Stack: "ai", Dir: dir, Why: oneLine(t.Why, 100)}
		if m := firstVersion(t.Min); m != "" {
			ck.Min = m
		}
		ck.Install = core.OSCommands{}
		for _, sys := range []string{"darwin", "linux", "windows"} {
			if cmd := oneLine(t.Install[sys], 200); cmd != "" && cmd == strings.TrimSpace(t.Install[sys]) {
				ck.Install[sys] = cmd
			}
		}
		if len(ck.Install) == 0 {
			ck.Install = nil
		}
		out = append(out, ck)
	}
	return out, nil
}

func oneLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, "\r\n") {
		return ""
	}
	if len(s) > limit {
		s = s[:limit]
	}
	return s
}

// askAIForMore shows what it would send, asks, and asks the AI. Any failure
// is reported and ignored: the local findings stand on their own.
func askAIForMore(c *core.Context, p *prompter, dir string, found []foundStack) []core.DoctorCheck {
	files := gatherContext(dir)
	if len(files) == 0 {
		return nil
	}
	provider, err := ensureProvider(c, p)
	if err != nil {
		fmt.Fprintf(c.Stderr, "skipping the AI step: %v\n", err)
		return nil
	}
	c.Printf("To suggest more, zx can ask %s what else this project needs. It would send:\n", provider.ID)
	for _, f := range files {
		note := ""
		if f.name == ".env.example" {
			note = "  (variable names only, no values)"
		}
		c.Printf("  %s  %d bytes%s\n", padRight(f.name, 26), len(f.text), note)
	}
	c.Println("  plus the names of the files in this folder. Secrets are redacted; .env is never read.")
	if answer, _ := p.ask("Send them? [y/N] "); !isYes(answer) {
		fmt.Fprintln(c.Stderr, "ok, only what zx recognised itself")
		return nil
	}
	skip := map[string]bool{}
	for _, name := range strings.Split(DefaultDoctorTools, ",") {
		skip[strings.TrimSpace(name)] = true
	}
	for _, f := range found {
		for _, ck := range f.checks {
			skip[ck.Tool] = true
		}
	}
	fmt.Fprintf(c.Stderr, "%s\n", c.App.Dim("asking "+provider.ID+"..."))
	reply, err := provider.Ask(c.App.Config, discoverPrompt(dir, found, files))
	if err != nil {
		fmt.Fprintf(c.Stderr, "the AI step failed: %v\n", askError(provider.ID, err))
		return nil
	}
	extra, err := parseSuggestions(reply, dir, skip)
	if err != nil {
		fmt.Fprintf(c.Stderr, "the AI's reply could not be used (%v), so only what zx recognised itself is shown\n", err)
		return nil
	}
	if len(extra) == 0 {
		c.Println("The AI found nothing more to add.")
	}
	return extra
}

// offerToSave keeps what discovery found, so a plain "zx doctor" checks it
// from now on. It writes only zx's own files under .zx, never your config.
func offerToSave(c *core.Context, p *prompter, cfg core.Config, dir string, found []foundStack, plan []core.DoctorCheck) {
	if !c.App.StdinTTY {
		return
	}
	var specs []string
	for _, f := range found {
		specs = append(specs, f.spec())
	}
	var extra []core.DoctorCheck
	for _, ck := range plan {
		if ck.Stack == "ai" && ck.Tool != "" {
			extra = append(extra, ck)
		}
	}
	if len(specs) == 0 && len(extra) == 0 {
		return
	}
	root := baseDir(cfg, dir)
	settings := core.ProjectSettingsPath(root)
	have := splitList(core.ReadSettings(settings)["doctor.stacks"])
	merged := append([]string{}, have...)
	for _, s := range specs {
		if !contains(merged, s) {
			merged = append(merged, s)
		}
	}
	existing, _ := core.LoadDoctorFile(filepath.Join(root, core.ProjectDirName, core.DoctorFileName), root)
	newTools := 0
	for _, e := range extra {
		if !hasTool(existing, e.Tool) {
			newTools++
		}
	}
	if len(merged) == len(have) && newTools == 0 {
		return
	}
	c.Println()
	answer, _ := p.ask("Save this, so a plain 'zx doctor' in this project checks it every time? [y/N] ")
	if !isYes(answer) {
		return
	}
	if len(merged) > len(have) {
		if err := core.SaveSetting(settings, "doctor.stacks", strings.Join(merged, ",")); err != nil {
			fmt.Fprintf(c.Stderr, "could not save: %v\n", err)
			return
		}
		c.Printf("saved %s\n", settings)
	}
	if newTools > 0 {
		path := filepath.Join(root, core.ProjectDirName, core.DoctorFileName)
		list := existing
		for _, e := range extra {
			if !hasTool(list, e.Tool) {
				list = append(list, core.DoctorCheck{Tool: e.Tool, Min: e.Min, Why: e.Why, Install: e.Install})
			}
		}
		if err := writeDoctorFile(path, list); err != nil {
			fmt.Fprintf(c.Stderr, "could not save: %v\n", err)
			return
		}
		c.Printf("saved %s\n", path)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func hasTool(list []core.DoctorCheck, tool string) bool {
	for _, d := range list {
		if d.Tool == tool {
			return true
		}
	}
	return false
}

func writeDoctorFile(path string, list []core.DoctorCheck) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(list); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
