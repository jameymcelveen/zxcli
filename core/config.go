package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

// ProjectFile is the per-project config file zx looks for in the current
// directory and its parents.
const ProjectFile = ".zx.json"

// Script is a user-defined command: a name and a shell line to run.
type Script struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Run     string `json:"run"`

	Dir    string `json:"-"` // working directory; empty means the current one
	Source string `json:"-"` // file the script was defined in
}

func (s Script) describe() string {
	if s.Summary != "" {
		return s.Summary
	}
	return s.Run
}

// configFile is the on-disk shape of a user or project config.
type configFile struct {
	Aliases  map[string]string `json:"aliases"`
	Commands []Script          `json:"commands"`
	Disabled []string          `json:"disabled"`
}

// Config is the merged user and project configuration.
type Config struct {
	Aliases     map[string]string
	Disabled    map[string]bool
	User        []Script
	Project     []Script
	UserPath    string // where the user config lives (it may not exist yet)
	ProjectPath string // empty when no .zx.json was found
}

// Script looks up a script command. Project scripts win over user scripts.
func (c Config) Script(name string) (Script, bool) {
	for _, list := range [][]Script{c.Project, c.User} {
		for _, s := range list {
			if s.Name == name {
				return s, true
			}
		}
	}
	return Script{}, false
}

// UserConfigPath returns the user config location: $ZX_CONFIG if set,
// otherwise ~/.config/zx/zxcli.json (%AppData%\zx\zxcli.json on Windows).
func UserConfigPath() string {
	if p := os.Getenv("ZX_CONFIG"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "zx", "zxcli.json")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("AppData"); d != "" {
			return filepath.Join(d, "zx", "zxcli.json")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "zx", "zxcli.json")
}

// FindProjectConfig walks up from dir looking for .zx.json.
func FindProjectConfig(dir string) string {
	for dir != "" {
		p := filepath.Join(dir, ProjectFile)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

var scriptName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

// LoadConfig reads the user config and the nearest project config. Problems
// are returned as warnings so a broken config never locks the user out.
func LoadConfig(cwd string) (Config, []string) {
	cfg := Config{Aliases: map[string]string{}, Disabled: map[string]bool{}, UserPath: UserConfigPath()}
	var warnings []string

	load := func(path string, project bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				warnings = append(warnings, err.Error())
			}
			return
		}
		var f configFile
		if err := json.Unmarshal(StripJSONC(data), &f); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			return
		}
		for k, v := range f.Aliases {
			cfg.Aliases[k] = v
		}
		for _, d := range f.Disabled {
			cfg.Disabled[d] = true
		}
		for _, s := range f.Commands {
			if !scriptName.MatchString(s.Name) || s.Run == "" {
				warnings = append(warnings, fmt.Sprintf("%s: skipping command %q: it needs a simple name and a \"run\" line", path, s.Name))
				continue
			}
			s.Source = path
			if project {
				s.Dir = filepath.Dir(path)
				cfg.Project = append(cfg.Project, s)
			} else {
				cfg.User = append(cfg.User, s)
			}
		}
	}

	if cfg.UserPath != "" {
		load(cfg.UserPath, false)
	}
	if p := FindProjectConfig(cwd); p != "" && p != cfg.UserPath {
		cfg.ProjectPath = p
		load(p, true)
	}
	return cfg, warnings
}

// StripJSONC turns "JSON with comments" into plain JSON. It removes // and
// /* */ comments and trailing commas, and leaves string contents untouched.
func StripJSONC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(in) && in[j] != '"' {
				if in[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(in) {
				j = len(in) - 1
			}
			out = append(out, in[i:j+1]...)
			i = j
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			i-- // keep the newline
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			i += 2
			for i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/') {
				i++
			}
			i++
		case c == ',':
			j := i + 1
			for j < len(in) {
				switch {
				case in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r':
					j++
					continue
				case in[j] == '/' && j+1 < len(in) && in[j+1] == '/':
					for j < len(in) && in[j] != '\n' {
						j++
					}
					continue
				case in[j] == '/' && j+1 < len(in) && in[j+1] == '*':
					j += 2
					for j+1 < len(in) && !(in[j] == '*' && in[j+1] == '/') {
						j++
					}
					j += 2
					continue
				}
				break
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				continue // trailing comma: drop it
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
