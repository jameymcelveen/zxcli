package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// ProjectDirName is the folder in a project that holds script commands
// (.zx/commands) and settings zx writes itself (.zx/settings.json).
const ProjectDirName = ".zx"

// LookPath finds a program on PATH. Tests replace it.
var LookPath = exec.LookPath

// FindProjectRoot walks up from dir to the nearest folder that has a .zx.json
// file or a .zx folder. It returns "" when there is none.
func FindProjectRoot(dir string) string {
	for dir != "" {
		if st, err := os.Stat(filepath.Join(dir, ProjectFile)); err == nil && !st.IsDir() {
			return dir
		}
		if st, err := os.Stat(filepath.Join(dir, ProjectDirName)); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// UserDir is the folder that holds the user config, settings and commands.
func UserDir() string {
	if p := UserConfigPath(); p != "" {
		return filepath.Dir(p)
	}
	return ""
}

// scriptLanguages maps a file extension to the language name zx uses for it.
var scriptLanguages = map[string]string{
	".sh": "sh", ".bash": "sh", ".js": "js", ".mjs": "js", ".cjs": "js", ".ts": "ts", ".py": "py", ".ps1": "ps1",
}

// LanguageExt returns the file extension for a language name, or "".
func LanguageExt(language string) string {
	switch language {
	case "sh", "js", "ts", "py", "ps1":
		return "." + language
	}
	return ""
}

var nodeVersion = regexp.MustCompile(`v(\d+)\.(\d+)`)

// FileRunner returns the program and leading arguments that run a script
// file, chosen by its extension from what is installed. The error says what
// to install when nothing suitable is found.
func FileRunner(path string) ([]string, error) {
	has := func(name string) bool { _, err := LookPath(name); return err == nil }
	ext := strings.ToLower(filepath.Ext(path))
	switch scriptLanguages[ext] {
	case "sh":
		if runtime.GOOS == "windows" {
			for _, sh := range []string{"bash", "sh"} {
				if has(sh) {
					return []string{sh}, nil
				}
			}
			return nil, errors.New("shell scripts need bash on Windows: install Git for Windows, or use a .ps1 or .js command")
		}
		if ext == ".bash" && has("bash") {
			return []string{"bash"}, nil
		}
		return []string{"sh"}, nil
	case "js":
		switch {
		case has("node"):
			return []string{"node"}, nil
		case has("bun"):
			return []string{"bun", "run"}, nil
		case has("deno"):
			return []string{"deno", "run", "-A"}, nil
		}
		return nil, errors.New("JavaScript commands need Node.js, Bun or Deno")
	case "ts":
		switch {
		case has("bun"):
			return []string{"bun", "run"}, nil
		case has("deno"):
			return []string{"deno", "run", "-A"}, nil
		case has("tsx"):
			return []string{"tsx"}, nil
		case has("node"):
			// Node runs TypeScript directly from 23.6 on.
			if out, err := exec.Command("node", "--version").Output(); err == nil {
				if m := nodeVersion.FindSubmatch(out); m != nil {
					major, _ := strconv.Atoi(string(m[1]))
					minor, _ := strconv.Atoi(string(m[2]))
					if major > 23 || (major == 23 && minor >= 6) {
						return []string{"node"}, nil
					}
				}
			}
		}
		return nil, errors.New("TypeScript commands need Bun, Deno, tsx, or Node.js 23.6 or newer")
	case "py":
		for _, py := range []string{"python3", "python", "py"} {
			if has(py) {
				return []string{py}, nil
			}
		}
		return nil, errors.New("Python commands need python3")
	case "ps1":
		for _, ps := range []string{"pwsh", "powershell"} {
			if has(ps) {
				return []string{ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}, nil
			}
		}
		return nil, errors.New("PowerShell commands need pwsh")
	}
	return nil, fmt.Errorf("zx does not know how to run %s files", ext)
}

// Languages lists the script languages this machine can run, in the order zx
// prefers them.
func Languages() []string {
	order := []string{"sh", "ps1", "js", "ts", "py"}
	if runtime.GOOS == "windows" {
		order = []string{"ps1", "sh", "js", "ts", "py"}
	}
	var out []string
	for _, lang := range order {
		if _, err := FileRunner("x." + lang); err == nil {
			out = append(out, lang)
		}
	}
	return out
}

// FileCommand builds the process that runs a script file with args. dir is
// the working directory; "" means the current one.
func FileCommand(ctx context.Context, path, dir string, args []string) (*exec.Cmd, error) {
	runner, err := FileRunner(path)
	if err != nil {
		return nil, err
	}
	argv := append(append(runner[1:], path), args...)
	cmd := exec.CommandContext(ctx, runner[0], argv...)
	cmd.Dir = dir
	return cmd, nil
}

// scriptCmd builds the process for any script command: a shell line or a file.
func scriptCmd(s Script, args []string) (*exec.Cmd, error) {
	if s.File != "" {
		return FileCommand(context.Background(), s.File, s.Dir, args)
	}
	return shellCmd(s, args), nil
}

var summaryLine = regexp.MustCompile(`(?i)^\s*(?:#|//|--|rem\b|::)\s*summary:\s*(.+?)\s*$`)

// scriptSummary reads the "summary:" comment near the top of a script file.
func scriptSummary(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < 15 && sc.Scan(); i++ {
		if m := summaryLine.FindStringSubmatch(sc.Text()); m != nil {
			return m[1]
		}
	}
	return ""
}

// loadCommandFiles turns every script in dir into a command named after the
// file. Files with names zx could not type as a command are ignored.
func loadCommandFiles(dir string) []Script {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Script
	seen := map[string]bool{}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		name := strings.TrimSuffix(e.Name(), ext)
		if e.IsDir() || scriptLanguages[strings.ToLower(ext)] == "" || !scriptName.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		path := filepath.Join(dir, e.Name())
		out = append(out, Script{Name: name, Summary: scriptSummary(path), File: path, Source: path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ReadSettings reads a settings.json: a flat object of string values
// that zx writes itself (as opposed to the config file, which you write).
func ReadSettings(path string) map[string]string {
	settings := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &settings)
	}
	return settings
}

// UserSettingsPath is the settings file zx writes for this machine.
func UserSettingsPath() string {
	if d := UserDir(); d != "" {
		return filepath.Join(d, "settings.json")
	}
	return ""
}

// ProjectSettingsPath is the settings file zx writes for the project at root.
func ProjectSettingsPath(root string) string {
	return filepath.Join(root, ProjectDirName, "settings.json")
}

// SaveSetting stores one value in a settings.json file. An empty value
// removes the key. Your hand-written config file is never touched.
func SaveSetting(path, key, value string) error {
	if path == "" {
		return errors.New("no settings file to write to")
	}
	settings := ReadSettings(path)
	if value == "" {
		delete(settings, key)
	} else {
		settings[key] = value
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
