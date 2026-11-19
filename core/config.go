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
	"strings"
)

// ProjectFile is the per-project config file zx looks for in the current
// directory and its parents.
const ProjectFile = ".zx.json"

// Script is a user-defined command: a name and a shell line to run.
type Script struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Run     string `json:"run"`
	Global  bool   `json:"global,omitempty"` // also runnable without the "zx" prefix

	File   string `json:"-"` // script file to run, for commands that live in a commands folder
	Dir    string `json:"-"` // working directory; empty means the current one
	Source string `json:"-"` // file the script was defined in
}

func (s Script) describe() string {
	switch {
	case s.Summary != "":
		return s.Summary
	case s.File != "":
		return filepath.Base(s.File)
	}
	return s.Run
}

// configFile is the on-disk shape of a user or project config.
type configFile struct {
	Aliases  map[string]aliasValue `json:"aliases"`
	Commands []Script              `json:"commands"`
	Disabled []string              `json:"disabled"`
	Settings map[string]string     `json:"settings"`
	Doctor   []DoctorCheck         `json:"doctor"`
}

// Config is the merged user and project configuration.
type Config struct {
	Aliases       map[string]string
	Disabled      map[string]bool
	Settings      map[string]string // free-form options read by individual commands
	UserDoctor    []DoctorCheck     // "doctor" list from the user config; nil when absent
	ProjectDoctor []DoctorCheck     // "doctor" list from the project config
	User          []Script
	Project       []Script
	Global        map[string]bool // aliases and commands marked "global": true in the user config
	UserPath      string          // where the user config lives (it may not exist yet)
	ProjectPath   string          // empty when no .zx.json was found
	ProjectDir    string          // the project root: the folder with .zx.json or .zx; empty when none
}

// Setting returns a value from the "settings" block, or def when it is unset.
func (c Config) Setting(key, def string) string {
	if v, ok := c.Settings[key]; ok && v != "" {
		return v
	}
	return def
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
	cfg := Config{Aliases: map[string]string{}, Disabled: map[string]bool{}, Settings: map[string]string{}, Global: map[string]bool{}, UserPath: UserConfigPath()}
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
			if !scriptName.MatchString(k) || strings.TrimSpace(v.Run) == "" {
				warnings = append(warnings, fmt.Sprintf("%s: skipping alias %q: it needs a simple name and a command", path, k))
				continue
			}
			cfg.Aliases[k] = v.Run
			delete(cfg.Global, k)
			switch {
			case v.Global && project:
				warnings = append(warnings, fmt.Sprintf("%s: alias %q can't be global: only your own config can add machine-wide commands", path, k))
			case v.Global:
				cfg.Global[k] = true
			}
		}
		for _, d := range f.Disabled {
			cfg.Disabled[d] = true
		}
		for k, v := range f.Settings {
			cfg.Settings[k] = v
		}
		if f.Doctor != nil && !project {
			cfg.UserDoctor = []DoctorCheck{}
		}
		for _, d := range f.Doctor {
			if err := d.validate(); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: skipping a doctor check: %v", path, err))
				continue
			}
			d.Source = path
			if project {
				d.Project, d.Dir = true, filepath.Dir(path)
				cfg.ProjectDoctor = append(cfg.ProjectDoctor, d)
			} else {
				cfg.UserDoctor = append(cfg.UserDoctor, d)
			}
		}
		for _, s := range f.Commands {
			if !scriptName.MatchString(s.Name) || s.Run == "" {
				warnings = append(warnings, fmt.Sprintf("%s: skipping command %q: it needs a simple name and a \"run\" line", path, s.Name))
				continue
			}
			s.Source = path
			switch {
			case s.Global && project:
				warnings = append(warnings, fmt.Sprintf("%s: command %q can't be global: only your own config can add machine-wide commands", path, s.Name))
				s.Global = false
			case s.Global:
				cfg.Global[s.Name] = true
			}
			if project {
				s.Dir = filepath.Dir(path)
				cfg.Project = append(cfg.Project, s)
			} else {
				cfg.User = append(cfg.User, s)
			}
		}
	}

	// Script files in a commands folder become commands too. An entry in the
	// config file with the same name wins.
	addFiles := func(dir, runIn string, project bool) {
		for _, s := range loadCommandFiles(dir) {
			s.Dir = runIn
			list := &cfg.User
			if project {
				list = &cfg.Project
			}
			taken := false
			for _, have := range *list {
				taken = taken || have.Name == s.Name
			}
			if !taken {
				*list = append(*list, s)
			}
		}
	}

	userDir := ""
	if cfg.UserPath != "" {
		userDir = filepath.Dir(cfg.UserPath)
		load(cfg.UserPath, false)
		for k, v := range ReadSettings(filepath.Join(userDir, "settings.json")) {
			cfg.Settings[k] = v
		}
		addFiles(filepath.Join(userDir, "commands"), "", false)
	}
	if root := FindProjectRoot(cwd); root != "" && root != userDir {
		cfg.ProjectDir = root
		if p := filepath.Join(root, ProjectFile); p != cfg.UserPath {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				cfg.ProjectPath = p
				load(p, true)
			}
		}
		for k, v := range ReadSettings(ProjectSettingsPath(root)) {
			cfg.Settings[k] = v
		}
		addFiles(filepath.Join(root, ProjectDirName, "commands"), root, true)
		extra, warn := LoadDoctorFile(filepath.Join(root, ProjectDirName, DoctorFileName), root)
		cfg.ProjectDoctor = append(cfg.ProjectDoctor, extra...)
		warnings = append(warnings, warn...)
	}
	return cfg, warnings
}

// DoctorFileName is the file in .zx that "zx doctor --discover" writes: a list
// of extra checks, in the same shape as the "doctor" list in a config file.
const DoctorFileName = "doctor.json"

// LoadDoctorFile reads a machine-written list of doctor checks. A missing
// file is not a problem. The checks count as the project's own, so a custom
// "run" check still needs "zx doctor --trust".
func LoadDoctorFile(path, root string) ([]DoctorCheck, []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var list []DoctorCheck
	if err := json.Unmarshal(StripJSONC(data), &list); err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	var out []DoctorCheck
	var warnings []string
	for _, d := range list {
		if err := d.validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: skipping a doctor check: %v", path, err))
			continue
		}
		d.Source, d.Project, d.Dir = path, true, root
		out = append(out, d)
	}
	return out, warnings
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

// DoctorCheck is one requirement that "zx doctor" verifies. It is either a
// tool that must be installed (optionally at a minimum version) or a named
// shell command that must succeed. In config it may be written as a bare
// string, which means a tool: "git".
type DoctorCheck struct {
	Tool    string     `json:"tool,omitempty"`
	Min     string     `json:"min,omitempty"`
	Name    string     `json:"name,omitempty"`
	Run     string     `json:"run,omitempty"`
	File    string     `json:"file,omitempty"` // a file or folder that must exist, relative to Dir
	Why     string     `json:"why,omitempty"`  // shown beside a check that fails
	Install OSCommands `json:"install,omitempty"`
	Upgrade OSCommands `json:"upgrade,omitempty"`

	Source  string `json:"-"` // file the check was defined in
	Dir     string `json:"-"` // working directory for Run, and the base for File
	Project bool   `json:"-"` // true when it came from a project's .zx.json
	Stack   string `json:"-"` // set when it came from a stack preset such as "laravel"
}

// UnmarshalJSON accepts "git" as shorthand for {"tool": "git"}.
func (d *DoctorCheck) UnmarshalJSON(b []byte) error {
	var tool string
	if json.Unmarshal(b, &tool) == nil {
		*d = DoctorCheck{Tool: tool}
		return nil
	}
	type plain DoctorCheck
	return json.Unmarshal(b, (*plain)(d))
}

func (d DoctorCheck) validate() error {
	switch {
	case d.Tool != "" && d.Run != "":
		return fmt.Errorf("%q has both \"tool\" and \"run\"; use one", d.Tool)
	case d.File != "" && (d.Tool != "" || d.Run != ""):
		return fmt.Errorf("%q has \"file\" together with \"tool\" or \"run\"; use one", d.File)
	case d.File != "":
		return nil
	case d.Tool != "" && !scriptName.MatchString(d.Tool):
		return fmt.Errorf("%q is not a program name", d.Tool)
	case d.Tool == "" && (d.Name == "" || d.Run == ""):
		return errors.New("it needs either \"tool\", or both \"name\" and \"run\"")
	}
	return nil
}

// Label is the name shown in the report.
func (d DoctorCheck) Label() string {
	switch {
	case d.Name != "":
		return d.Name
	case d.Tool != "":
		return d.Tool
	}
	return d.File
}

// OSCommands is a shell command per operating system. In config it is either
// one string used everywhere, or an object keyed by "mac", "windows" and
// "linux".
type OSCommands map[string]string

// UnmarshalJSON accepts a plain string as "the same command on every OS".
func (o *OSCommands) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*o = OSCommands{"all": one}
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return errors.New("\"install\" must be a command, or an object with mac, windows and linux commands")
	}
	*o = m
	return nil
}

// For returns the command for an operating system (a GOOS value), or "".
func (o OSCommands) For(goos string) string {
	keys := []string{goos}
	if goos == "darwin" {
		keys = append(keys, "mac", "macos")
	}
	for _, k := range append(keys, "all") {
		if v := o[k]; v != "" {
			return v
		}
	}
	return ""
}

// aliasValue is an alias as written in config: either the command it stands
// for ("guid --copy"), or an object that can also mark it global:
// {"run": "doctor --fix", "global": true}.
type aliasValue struct {
	Run    string `json:"run"`
	Global bool   `json:"global"`
}

func (a *aliasValue) UnmarshalJSON(b []byte) error {
	var run string
	if json.Unmarshal(b, &run) == nil {
		*a = aliasValue{Run: run}
		return nil
	}
	type plain aliasValue
	if err := json.Unmarshal(b, (*plain)(a)); err != nil {
		return errors.New("an alias must be a command, or an object like {\"run\": \"doctor --fix\", \"global\": true}")
	}
	return nil
}

// GlobalDir is where the launchers for global commands live: $ZX_BIN, or a
// "bin" folder beside the user config.
func GlobalDir() string {
	if d := os.Getenv("ZX_BIN"); d != "" {
		return d
	}
	if p := UserConfigPath(); p != "" {
		return filepath.Join(filepath.Dir(p), "bin")
	}
	return ""
}
