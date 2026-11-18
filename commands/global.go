package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("global", runGlobal) }

// globalMarker is written into every launcher, so zx only ever overwrites or
// deletes files it created.
const globalMarker = "zx global command"

// GlobalEntry is one alias or command marked "global": true.
type GlobalEntry struct {
	Name   string `json:"name"`
	Runs   string `json:"runs"`
	State  string `json:"state"` // linked, not linked or taken
	Detail string `json:"detail,omitempty"`
}

// shellBuiltins are names a shell handles itself, so no file on PATH would
// reveal that they are taken.
var shellBuiltins = map[string]bool{
	"alias": true, "bg": true, "break": true, "builtin": true, "case": true, "cd": true, "command": true,
	"continue": true, "declare": true, "dirs": true, "do": true, "done": true, "elif": true, "else": true,
	"esac": true, "eval": true, "exec": true, "exit": true, "export": true, "fc": true, "fg": true, "fi": true,
	"for": true, "function": true, "hash": true, "history": true, "if": true, "in": true, "jobs": true,
	"let": true, "local": true, "logout": true, "popd": true, "pushd": true, "read": true, "readonly": true,
	"return": true, "select": true, "set": true, "shift": true, "source": true, "then": true, "time": true,
	"trap": true, "type": true, "typeset": true, "ulimit": true, "umask": true, "unalias": true, "unset": true,
	"until": true, "wait": true, "while": true,
	// cmd.exe and PowerShell
	"assoc": true, "call": true, "cls": true, "copy": true, "del": true, "dir": true, "echo": true,
	"endlocal": true, "erase": true, "goto": true, "md": true, "mkdir": true, "mklink": true, "move": true,
	"path": true, "pause": true, "prompt": true, "rd": true, "rem": true, "ren": true, "rename": true,
	"rmdir": true, "setlocal": true, "start": true, "title": true, "ver": true, "verify": true, "vol": true,
	"zx": true,
}

func runGlobal(c *core.Context) error {
	fs := c.Flags()
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	dir := core.GlobalDir()
	if dir == "" {
		return errors.New("can't work out where global commands should live; set ZX_BIN")
	}
	sub := ""
	if len(pos) > 0 {
		sub = pos[0]
	}
	switch sub {
	case "path":
		c.Println(dir)
		return nil
	case "sync":
		changes, err := SyncGlobals(c.App.Config, dir)
		if err != nil {
			return err
		}
		for _, line := range changes {
			c.Println(line)
		}
	case "":
	default:
		return core.Usagef("unknown subcommand %q: use sync or path", sub)
	}

	entries := GlobalEntries(c.App.Config, dir)
	if *asJSON {
		if entries == nil {
			entries = []GlobalEntry{}
		}
		return c.JSON(entries)
	}
	if len(entries) == 0 {
		c.Println(`no global commands yet. Mark an alias or command with "global": true in your config:`)
		c.Println(`  "aliases": { "bootstrap": { "run": "doctor --fix", "global": true } }`)
		c.Println("then run: zx global sync")
		return nil
	}
	nameW := 0
	for _, e := range entries {
		nameW = max(nameW, len(e.Name))
	}
	pending, taken := 0, 0
	for _, e := range entries {
		state := padRight(e.State, len("not linked"))
		switch e.State {
		case "linked":
			state = c.App.Cyan(state)
		case "taken":
			taken++
			state = c.App.Bold(state)
		default:
			pending++
		}
		line := state + "  " + padRight(e.Name, nameW) + "  " + e.Runs
		if e.Detail != "" {
			line += "  " + c.App.Dim(e.Detail)
		}
		c.Println(line)
	}
	if pending > 0 {
		c.Println("\nrun 'zx global sync' to link them")
	}
	if !dirOnPath(dir) {
		c.Printf("\n%s is not on your PATH yet, so these will not run until you add it:\n  %s\n", dir, pathHint(dir))
	}
	if taken > 0 {
		c.Println("\na name that is taken was left alone. Pick another name for it in your config.")
		if sub == "sync" {
			return &core.ExitError{Code: 1}
		}
	}
	return nil
}

// globalTargets maps each global name to what it runs, for display.
func globalTargets(cfg core.Config) map[string]string {
	targets := map[string]string{}
	for name := range cfg.Global {
		if exp, ok := cfg.Aliases[name]; ok {
			targets[name] = "zx " + exp
		} else if s, ok := cfg.Script(name); ok {
			targets[name] = s.Run
		}
	}
	return targets
}

// GlobalEntries reports the state of every global command.
func GlobalEntries(cfg core.Config, dir string) []GlobalEntry {
	targets := globalTargets(cfg)
	names := make([]string, 0, len(targets))
	for n := range targets {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []GlobalEntry
	for _, n := range names {
		e := GlobalEntry{Name: n, Runs: targets[n], State: "not linked"}
		if by := NameTakenBy(n, dir); by != "" {
			e.State, e.Detail = "taken", "already "+by
		} else if isOurLauncher(launcherPath(dir, n)) {
			e.State = "linked"
		}
		out = append(out, e)
	}
	return out
}

func launcherPath(dir, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, name+".cmd")
	}
	return filepath.Join(dir, name)
}

func launcherBody(name string) string {
	if runtime.GOOS == "windows" {
		return "@echo off\r\nrem " + globalMarker + `. Managed by "zx global sync"; edits are overwritten.` + "\r\nzx " + name + " %*\r\n"
	}
	return "#!/bin/sh\n# " + globalMarker + `. Managed by "zx global sync"; edits are overwritten.` + "\nexec zx " + name + ` "$@"` + "\n"
}

func isOurLauncher(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), globalMarker)
}

// NameTakenBy says what already owns a command name: "a shell built-in", or
// "a command: /path/to/it". It returns "" when the name is free. Launchers in
// dir (zx's own) do not count.
func NameTakenBy(name, dir string) string {
	if shellBuiltins[strings.ToLower(name)] {
		return "a shell built-in"
	}
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = strings.Split(strings.ToLower(os.Getenv("PATHEXT")), ";")
		if len(exts) < 2 {
			exts = []string{".com", ".exe", ".bat", ".cmd"}
		}
	}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d == "" || sameDir(d, dir) {
			continue
		}
		for _, ext := range exts {
			p := filepath.Join(d, name+ext)
			st, err := os.Stat(p)
			if err != nil || st.IsDir() {
				continue
			}
			if runtime.GOOS == "windows" || st.Mode()&0o111 != 0 {
				return "a command: " + p
			}
		}
	}
	return ""
}

func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	if realA, err := filepath.EvalSymlinks(absA); err == nil {
		absA = realA
	}
	if realB, err := filepath.EvalSymlinks(absB); err == nil {
		absB = realB
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(absA, absB)
	}
	return absA == absB
}

func dirOnPath(dir string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d != "" && sameDir(d, dir) {
			return true
		}
	}
	return false
}

func pathHint(dir string) string {
	if runtime.GOOS == "windows" {
		return `[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';` + dir + `', 'User')   (PowerShell, then open a new terminal)`
	}
	shown := dir
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home+string(filepath.Separator)) {
		shown = "$HOME" + dir[len(home):]
	}
	if strings.Contains(os.Getenv("SHELL"), "fish") {
		return "fish_add_path " + shown
	}
	profile := "~/.zshrc"
	if strings.Contains(os.Getenv("SHELL"), "bash") {
		profile = "~/.bashrc"
	}
	return `echo 'export PATH="` + shown + `:$PATH"' >> ` + profile + "   (then open a new terminal)"
}

// SyncGlobals makes the launchers in dir match the config: it writes one for
// every free global name, and removes zx's own launchers for names that are
// no longer global or are now taken. It never touches a file it did not
// create. The returned lines say what changed.
func SyncGlobals(cfg core.Config, dir string) ([]string, error) {
	targets := globalTargets(cfg)
	var changes []string
	if len(targets) > 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	want := map[string]bool{}
	names := make([]string, 0, len(targets))
	for n := range targets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		path := launcherPath(dir, n)
		if by := NameTakenBy(n, dir); by != "" {
			continue // reported by the status list; any old launcher is removed below
		}
		if fileExists(path) && !isOurLauncher(path) {
			return changes, fmt.Errorf("%s exists and was not made by zx, so it was left alone", path)
		}
		want[filepath.Base(path)] = true
		body := launcherBody(n)
		if old, err := os.ReadFile(path); err == nil && string(old) == body {
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			return changes, err
		}
		changes = append(changes, "linked "+n)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return changes, nil
		}
		return changes, err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() || want[e.Name()] || !isOurLauncher(path) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return changes, err
		}
		changes = append(changes, "removed "+strings.TrimSuffix(e.Name(), ".cmd"))
	}
	return changes, nil
}
