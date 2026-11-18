package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("new", runNew) }

const newCommandTemplate = `package commands

import (
	"strings"

	"MODULE/core"
)

func init() { core.Register("NAME", runIDENT) }

// runIDENT implements "zx NAME". Its summary, aliases and examples live in
// zxcli.json.
func runIDENT(c *core.Context) error {
	fs := c.Flags()
	loud := fs.Bool("loud", false, "shout it")
	args, err := c.Parse(fs)
	if err != nil {
		return err
	}
	msg := "hello from NAME"
	if len(args) > 0 {
		msg = strings.Join(args, " ")
	}
	if *loud {
		msg = strings.ToUpper(msg)
	}
	c.Println(msg)
	return nil
}
`

const newTestTemplate = `package commands

import (
	"strings"
	"testing"
)

func TestIDENT(t *testing.T) {
	out, _, err := run(t, "", "NAME", "--loud", "it", "works")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "IT WORKS" {
		t.Errorf("got %q, want %q", got, "IT WORKS")
	}
}
`

var newName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func runNew(c *core.Context) error {
	fs := c.Flags()
	summary := fs.String("s", "", "one-line `summary` shown in help")
	group := fs.String("g", "Commands", "help `group` to list it under")
	useAI := fs.Bool("ai", false, "have the AI write the command from your description")
	forUser := fs.Bool("user", false, "with a description: save it for you everywhere, not for this project")
	forProject := fs.Bool("project", false, "with a description: save it in this folder's .zx/commands")
	yes := fs.Bool("yes", false, "with a description: save without asking")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	// A description instead of a name means "write it for me":
	// zx new "bump the version and tag the repo"
	if request := strings.TrimSpace(strings.Join(pos, " ")); request != "" && (*useAI || strings.ContainsAny(request, " \t")) {
		scope := ""
		switch {
		case *forUser:
			scope = "user"
		case *forProject:
			scope = "project"
		}
		return aiNew(c, request, scope, *yes)
	}
	if len(pos) != 1 {
		return core.Usagef("give the new command a name (zx new dice), or describe it (zx new \"roll two dice\")")
	}
	cwd, err := workDir()
	if err != nil {
		return err
	}
	files, err := Scaffold(cwd, pos[0], *summary, *group)
	if err != nil {
		return err
	}
	for _, f := range files {
		if rel, err := filepath.Rel(cwd, f); err == nil {
			f = rel
		}
		c.Printf("wrote %s\n", f)
	}
	c.Printf("next: edit the command, then run: make test && make install\n")
	return nil
}

// Scaffold adds a new built-in command to the zxcli source tree that contains
// startDir: a Go file, a test, and a zxcli.json entry. It returns the files
// it wrote or changed.
func Scaffold(startDir, name, summary, group string) ([]string, error) {
	if !newName.MatchString(name) {
		return nil, core.Usagef("command names are lowercase letters, digits and dashes, starting with a letter; got %q", name)
	}
	root, module, err := findRepo(startDir)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(root, "zxcli.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var m core.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("zxcli.json: %w", err)
	}
	for _, cmd := range m.Commands {
		if cmd.Name == name {
			return nil, fmt.Errorf("zxcli.json already has a command named %q", name)
		}
		for _, a := range cmd.Aliases {
			if a == name {
				return nil, fmt.Errorf("%q is already an alias for %q", name, cmd.Name)
			}
		}
	}

	base := strings.ReplaceAll(name, "-", "_")
	goFile := filepath.Join(root, "commands", base+".go")
	testFile := filepath.Join(root, "commands", base+"_test.go")
	for _, f := range []string{goFile, testFile} {
		if fileExists(f) {
			return nil, fmt.Errorf("%s already exists", f)
		}
	}
	fill := strings.NewReplacer("MODULE", module, "NAME", name, "IDENT", ConvertCase("pascal", name))
	if err := os.WriteFile(goFile, []byte(fill.Replace(newCommandTemplate)), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(testFile, []byte(fill.Replace(newTestTemplate)), 0o644); err != nil {
		return nil, err
	}

	if summary == "" {
		summary = "TODO: describe " + name
	}
	entry := core.Meta{Name: name, Summary: summary, Usage: "zx " + name + " [text] [flags]", Group: group, Examples: []string{"zx " + name}}
	// Keep the hidden plumbing commands at the end of the list.
	at := len(m.Commands)
	for at > 0 && m.Commands[at-1].Hidden {
		at--
	}
	m.Commands = append(m.Commands[:at], append([]core.Meta{entry}, m.Commands[at:]...)...)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := os.WriteFile(manifestPath, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}
	return []string{goFile, testFile, manifestPath}, nil
}

var moduleLine = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// findRepo walks up from dir to the zxcli source tree: the folder that holds
// go.mod, zxcli.json and commands/.
func findRepo(dir string) (root, module string, err error) {
	for {
		mod, modErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if modErr == nil && fileExists(filepath.Join(dir, "zxcli.json")) && fileExists(filepath.Join(dir, "commands")) {
			if m := moduleLine.FindSubmatch(mod); m != nil {
				return dir, string(m[1]), nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", errors.New("run this inside the zxcli source folder (the one with go.mod and zxcli.json)")
		}
		dir = parent
	}
}
