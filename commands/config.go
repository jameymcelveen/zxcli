package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("config", runConfig) }

// StarterConfig is what "zx config init" writes.
const StarterConfig = `// zx user config. Comments and trailing commas are fine in this file.
{
  // Short names for things you type a lot. "zx g" runs "zx guid".
  "aliases": {
    "g": "guid",
    "id": "guid --copy",
  },

  // Your own commands. "run" is a shell line, and any extra arguments are
  // passed along to it. Try: zx gs
  "commands": [
    { "name": "gs", "summary": "git status, short form", "run": "git status -sb" },
    { "name": "myip", "summary": "public IP address", "run": "curl -s https://api.ipify.org && echo" },
  ],

  // Built-in commands you want hidden, for example ["lorem"].
  "disabled": [],

  // Options for individual commands.
  "settings": {
    // "repos.dir": "~/dev",                  // where "zx repos" looks
  },

  // What "zx doctor" checks on this machine. A project can add its own list
  // in its .zx.json. Without this list, zx checks a default set of tools.
  // "doctor": [
  //   "git",
  //   { "tool": "dotnet", "min": "10.0" },
  //   { "tool": "terraform", "install": { "mac": "brew install terraform", "windows": "winget install -e --id Hashicorp.Terraform" } },
  //   { "name": "VPN is up", "run": "ping -c 1 intranet.example.com" },
  // ],
}
`

func runConfig(c *core.Context) error {
	pos, err := c.Parse(c.Flags())
	if err != nil {
		return err
	}
	cfg := c.App.Config
	path := cfg.UserPath
	if path == "" {
		path = core.UserConfigPath()
	}
	sub := ""
	if len(pos) > 0 {
		sub = pos[0]
	}
	switch sub {
	case "":
		state := ""
		if !fileExists(path) {
			state = "  (not created yet; run: zx config init)"
		}
		c.Printf("user config     %s%s\n", path, state)
		if cfg.ProjectPath != "" {
			c.Printf("project config  %s\n", cfg.ProjectPath)
		} else {
			c.Printf("project config  none found (put a %s in a project to give it its own commands)\n", core.ProjectFile)
		}
		c.Printf("aliases %d, your commands %d, project commands %d, disabled %d\n",
			len(cfg.Aliases), len(cfg.User), len(cfg.Project), len(cfg.Disabled))
	case "path":
		c.Println(path)
	case "init":
		created, err := writeStarterConfig(path)
		if err != nil {
			return err
		}
		if !created {
			c.Printf("%s already exists, so it was left alone\n", path)
			return nil
		}
		c.Printf("created %s\ntry it: zx gs\n", path)
	case "edit":
		if _, err := writeStarterConfig(path); err != nil {
			return err
		}
		return openEditor(c, path)
	default:
		return core.Usagef("unknown subcommand %q: use init, edit or path", sub)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeStarterConfig creates the user config if it is missing. It never
// overwrites an existing file.
func writeStarterConfig(path string) (created bool, err error) {
	if path == "" {
		return false, errors.New("can't work out where your config lives; set ZX_CONFIG")
	}
	if fileExists(path) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(StarterConfig), 0o644)
}

func openEditor(c *core.Context, path string) error {
	var argv []string
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := strings.Fields(os.Getenv(env)); len(v) > 0 {
			argv = v
			break
		}
	}
	if argv == nil {
		switch runtime.GOOS {
		case "darwin":
			argv = []string{"open", "-t"}
		case "windows":
			argv = []string{"notepad"}
		default:
			argv = []string{"vi"}
		}
	}
	cmd := exec.Command(argv[0], append(argv[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("opening %s with %s: %w", path, argv[0], err)
	}
	return nil
}
