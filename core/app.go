package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// App is one run of the zx program.
type App struct {
	Manifest Manifest
	Config   Config
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	StdinTTY bool // true when stdin is a terminal, so there is nothing piped in
	Color    bool

	index map[string]Meta // command name or alias -> meta, enabled commands only
}

// New builds an App from the contents of zxcli.json.
func New(manifest []byte) (*App, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(manifest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("zxcli.json: %w", err)
	}
	a := &App{Manifest: m, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	a.StdinTTY = isTerminal(os.Stdin)
	a.Color = colorEnabled(os.Stdout)
	if err := a.reindex(); err != nil {
		return nil, err
	}
	return a, nil
}

// SetConfig applies user and project settings.
func (a *App) SetConfig(cfg Config) error {
	a.Config = cfg
	return a.reindex()
}

func (a *App) reindex() error {
	a.index = map[string]Meta{}
	for _, m := range a.Manifest.Commands {
		if m.Name == "" {
			return errors.New("zxcli.json: a command has no name")
		}
		if m.Disabled || a.Config.Disabled[m.Name] {
			continue
		}
		if _, ok := handlers[m.Name]; !ok {
			continue // listed in the manifest but not compiled in
		}
		for _, n := range append([]string{m.Name}, m.Aliases...) {
			if prev, dup := a.index[n]; dup {
				return fmt.Errorf("zxcli.json: %q is used by both %q and %q", n, prev.Name, m.Name)
			}
			a.index[n] = m
		}
	}
	return nil
}

// Commands returns the enabled built-in commands in manifest order.
func (a *App) Commands() []Meta {
	var out []Meta
	for _, m := range a.Manifest.Commands {
		if got, ok := a.index[m.Name]; ok && got.Name == m.Name {
			out = append(out, m)
		}
	}
	return out
}

// Run loads config, runs the command line, and returns the exit code.
func (a *App) Run(args []string) int {
	cwd, _ := os.Getwd()
	cfg, warnings := LoadConfig(cwd)
	if err := a.SetConfig(cfg); err != nil {
		warnings = append(warnings, err.Error())
	}
	for _, w := range warnings {
		fmt.Fprintln(a.Stderr, "zx: warning:", w)
	}
	return a.Exit(a.Dispatch(args))
}

// Exit reports err to the user and turns it into an exit code.
func (a *App) Exit(err error) int {
	var ue *UsageError
	var ee *ExitError
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ee):
		return ee.Code
	case errors.As(err, &ue):
		fmt.Fprintf(a.Stderr, "zx: %s\n", ue.Msg)
		if ue.Cmd != "" {
			fmt.Fprintf(a.Stderr, "Run 'zx help %s' for usage.\n", ue.Cmd)
		} else {
			fmt.Fprintln(a.Stderr, "Run 'zx help' to see the commands.")
		}
		return 2
	default:
		fmt.Fprintf(a.Stderr, "zx: %v\n", err)
		return 1
	}
}

// Dispatch runs one command line (without the leading "zx").
func (a *App) Dispatch(args []string) error {
	copyOut := false
	i := 0
globals:
	for ; i < len(args); i++ {
		switch args[i] {
		case "-c", "--copy":
			copyOut = true
		case "--no-color":
			a.Color = false
		case "-h", "--help":
			return a.exec("help", args[i+1:], false)
		case "-v", "--version":
			return a.exec("version", nil, false)
		default:
			break globals
		}
	}
	args = args[i:]
	if len(args) == 0 {
		return a.exec("help", nil, false)
	}
	if strings.HasPrefix(args[0], "-") {
		return Usagef("unknown flag %s", args[0])
	}
	name, rest := args[0], args[1:]
	// A sentence where a command name should be is a request for the AI:
	// zx "bump the version and tag the repo"
	if strings.ContainsAny(strings.TrimSpace(name), " \t\n") {
		name, rest = "__ask", args
	}
	if expansion, ok := a.Config.Aliases[name]; ok {
		if f := strings.Fields(expansion); len(f) > 0 {
			name, rest = f[0], append(f[1:], rest...)
		}
	}
	rest, wantsCopy := stripCopy(rest)
	return a.exec(name, rest, copyOut || wantsCopy)
}

// stripCopy removes "--copy" from args (anything after "--" is left alone).
func stripCopy(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--copy" {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

func (a *App) exec(name string, args []string, copyOut bool) error {
	out := a.Stdout
	var buf bytes.Buffer
	if copyOut {
		out = io.MultiWriter(a.Stdout, &buf)
	}
	if err := a.route(name, args, out); err != nil {
		return err
	}
	if copyOut {
		if err := Copy(strings.TrimRight(buf.String(), "\r\n")); err != nil {
			return fmt.Errorf("copy to clipboard: %w", err)
		}
		fmt.Fprintln(a.Stderr, a.Dim("copied to clipboard"))
	}
	return nil
}

// route finds what "name" means and runs it. Order: built-in command,
// project script (.zx.json), user script, then a zx-<name> program on PATH.
func (a *App) route(name string, args []string, out io.Writer) error {
	if m, ok := a.index[name]; ok {
		return handlers[m.Name](&Context{App: a, Meta: m, Args: args, Stdin: a.Stdin, Stdout: out, Stderr: a.Stderr})
	}
	if s, ok := a.Config.Script(name); ok {
		cmd, err := scriptCmd(s, args)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return runCmd(cmd, a.Stdin, out, a.Stderr)
	}
	if path, err := exec.LookPath("zx-" + name); err == nil {
		return runCmd(exec.Command(path, args...), a.Stdin, out, a.Stderr)
	}
	msg := fmt.Sprintf("unknown command %q", name)
	if s := a.Suggest(name); s != "" {
		msg += fmt.Sprintf(". Did you mean %q?", s)
	}
	return Usagef("%s", msg)
}

// Names returns every name the user can type: built-ins, scripts and plugins.
func (a *App) Names() []Item {
	var items []Item
	seen := map[string]bool{}
	add := func(name, summary string) {
		if !seen[name] {
			seen[name] = true
			items = append(items, Item{name, summary})
		}
	}
	for _, m := range a.Commands() {
		if !m.Hidden {
			add(m.Name, m.Summary)
		}
	}
	for _, s := range a.Config.Project {
		add(s.Name, s.Summary)
	}
	for _, s := range a.Config.User {
		add(s.Name, s.Summary)
	}
	for _, p := range ExternalPlugins() {
		add(p, "plugin")
	}
	return items
}

// Item is a command name with its one-line summary.
type Item struct{ Name, Summary string }

// Help prints the top-level command list.
func (a *App) Help(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n", a.Bold("zx"), a.Dim(Version))
	if a.Manifest.Tagline != "" {
		fmt.Fprintln(w, a.Manifest.Tagline)
	}
	fmt.Fprintf(w, "\n%s\n  zx <command> [arguments] [--copy]\n", a.Bold("Usage"))
	if _, ok := a.index["__ask"]; ok {
		fmt.Fprintf(w, "  zx \"say what you want done\"   %s\n", a.Dim("the AI writes a script, shows it, and asks before running"))
	}

	var groups []string
	byGroup := map[string][]Item{}
	width := 0
	note := func(group, name, summary string) {
		if _, ok := byGroup[group]; !ok {
			groups = append(groups, group)
		}
		byGroup[group] = append(byGroup[group], Item{name, summary})
		if len(name) > width {
			width = len(name)
		}
	}
	for _, m := range a.Commands() {
		if m.Hidden {
			continue
		}
		g := m.Group
		if g == "" {
			g = "Commands"
		}
		s := m.Summary
		if len(m.Aliases) > 0 {
			s += a.Dim("  (" + strings.Join(m.Aliases, ", ") + ")")
		}
		note(g, m.Name, s)
	}
	for _, s := range a.Config.Project {
		note("Project commands  "+a.Config.ProjectDir, s.Name, s.describe())
	}
	for _, s := range a.Config.User {
		summary := s.describe()
		if s.Global {
			summary += a.Dim("  (global)")
		}
		note("Your commands  "+a.Config.UserPath, s.Name, summary)
	}
	if len(a.Config.Aliases) > 0 {
		names := make([]string, 0, len(a.Config.Aliases))
		for n := range a.Config.Aliases {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			target := "zx " + a.Config.Aliases[n]
			if a.Config.Global[n] {
				target += a.Dim("  (global)")
			}
			note("Your aliases", n, target)
		}
	}
	for _, p := range ExternalPlugins() {
		note("Plugins  zx-* programs on your PATH", p, "zx-"+p)
	}
	for _, g := range groups {
		fmt.Fprintf(w, "\n%s\n", a.Bold(g))
		for _, it := range byGroup[g] {
			fmt.Fprintf(w, "  %s  %s\n", a.Cyan(fmt.Sprintf("%-*s", width, it.Name)), it.Summary)
		}
	}
	fmt.Fprintf(w, "\nRun %s for details on one command.\n", a.Cyan("zx help <command>"))
	fmt.Fprintf(w, "Add %s to any command to put its output on the clipboard.\n", a.Cyan("--copy"))
}

// HelpFor prints help for one command, script or plugin.
func (a *App) HelpFor(name string, w io.Writer) error {
	if _, ok := a.index[name]; ok {
		err := a.route(name, []string{"-h"}, w)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if s, ok := a.Config.Script(name); ok {
		fmt.Fprintf(w, "%s  %s\n\n", a.Bold("zx "+s.Name), s.Summary)
		runs := s.Run
		if s.File != "" {
			runs = s.File
			if runner, err := FileRunner(s.File); err == nil {
				runs = strings.Join(runner, " ") + " " + s.File
			}
		}
		fmt.Fprintf(w, "%s\n  %s\n\n%s\n  %s\n", a.Bold("Runs"), runs, a.Bold("Defined in"), s.Source)
		return nil
	}
	if exp, ok := a.Config.Aliases[name]; ok {
		fmt.Fprintf(w, "%s is an alias for %s\n", a.Bold("zx "+name), a.Cyan("zx "+exp))
		return nil
	}
	if path, err := exec.LookPath("zx-" + name); err == nil {
		return runCmd(exec.Command(path, "--help"), a.Stdin, w, a.Stderr)
	}
	return a.route(name, nil, w) // produces the "unknown command" error
}

// HiddenFlag starts the usage text of a flag that still works but is left out
// of the help, such as an old name kept for people who already use it.
const HiddenFlag = "(hidden) "

func (a *App) commandHelp(w io.Writer, m Meta, fs *flag.FlagSet) {
	fmt.Fprintf(w, "%s  %s\n", a.Bold("zx "+m.Name), m.Summary)
	usage := m.Usage
	if usage == "" {
		usage = "zx " + m.Name + " [flags]"
	}
	fmt.Fprintf(w, "\n%s\n  %s\n", a.Bold("Usage"), usage)
	if len(m.Aliases) > 0 {
		fmt.Fprintf(w, "\n%s\n  %s\n", a.Bold("Aliases"), strings.Join(m.Aliases, ", "))
	}

	type row struct{ left, right string }
	var rows []row
	width := 0
	fs.VisitAll(func(f *flag.Flag) {
		if strings.HasPrefix(f.Usage, HiddenFlag) {
			return
		}
		kind, usage := flag.UnquoteUsage(f)
		left := "-" + f.Name
		if len(f.Name) > 1 {
			left = "--" + f.Name
		}
		if kind != "" {
			left += " " + kind
		}
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
			usage += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		rows = append(rows, row{left, usage})
	})
	rows = append(rows, row{"--copy", "also put the output on the clipboard"})
	for _, r := range rows {
		if len(r.left) > width {
			width = len(r.left)
		}
	}
	fmt.Fprintf(w, "\n%s\n", a.Bold("Flags"))
	for _, r := range rows {
		fmt.Fprintf(w, "  %s  %s\n", a.Cyan(fmt.Sprintf("%-*s", width, r.left)), r.right)
	}
	if len(m.Examples) > 0 {
		fmt.Fprintf(w, "\n%s\n", a.Bold("Examples"))
		for _, e := range m.Examples {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
}

// Suggest returns the closest known command name, or "" when nothing is close.
func (a *App) Suggest(name string) string {
	best, bestDist := "", 3
	for _, it := range a.Names() {
		if strings.HasPrefix(it.Name, name) && len(name) >= 2 {
			return it.Name
		}
		if d := editDistance(name, it.Name); d < bestDist {
			best, bestDist = it.Name, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
