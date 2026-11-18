// Package core is the small framework behind zx: the command registry,
// the dispatcher, config loading, and help output.
package core

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Build information, set with -ldflags at release time.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Meta describes one built-in command. It is read from zxcli.json.
type Meta struct {
	Name     string   `json:"name"`
	Summary  string   `json:"summary"`
	Usage    string   `json:"usage,omitempty"`
	Group    string   `json:"group,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
	Examples []string `json:"examples,omitempty"`
	Hidden   bool     `json:"hidden,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

// Manifest is the shape of zxcli.json.
type Manifest struct {
	Tagline  string `json:"tagline,omitempty"`
	Commands []Meta `json:"commands"`
}

// RunFunc is the body of a command.
type RunFunc func(c *Context) error

var handlers = map[string]RunFunc{}

// Register binds a command name to its implementation. Each command file
// calls it from init(). The name must also appear in zxcli.json.
func Register(name string, run RunFunc) {
	if _, dup := handlers[name]; dup {
		panic("zx: command registered twice: " + name)
	}
	handlers[name] = run
}

// Registered returns the names of every command compiled into the binary.
func Registered() []string {
	names := make([]string, 0, len(handlers))
	for n := range handlers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// UsageError means the user called a command the wrong way. Exit code 2.
type UsageError struct {
	Msg string
	Cmd string
}

func (e *UsageError) Error() string { return e.Msg }

// Usagef builds a UsageError.
func Usagef(format string, a ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, a...)}
}

// ExitError carries the exit status of a script command or external plugin.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Context is what a command receives when it runs.
type Context struct {
	App    *App
	Meta   Meta
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Flags returns an empty flag set for the command. Define flags on it, then
// call Parse.
func (c *Context) Flags() *flag.FlagSet {
	fs := flag.NewFlagSet(c.Meta.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// Parse parses c.Args and returns the positional arguments. Unlike the
// standard library it accepts flags after positionals ("zx guid 5 -u").
// Everything after "--" is positional. "-h" prints the command's help.
func (c *Context) Parse(fs *flag.FlagSet) ([]string, error) {
	args, tail := c.Args, []string(nil)
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				c.App.commandHelp(c.Stdout, c.Meta, fs)
				return nil, flag.ErrHelp
			}
			return nil, &UsageError{Msg: err.Error(), Cmd: c.Meta.Name}
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	return append(pos, tail...), nil
}

// Input returns the text a command should work on: the positional arguments
// joined by spaces, or everything on stdin when there are none.
func (c *Context) Input(pos []string) ([]byte, error) {
	if len(pos) > 0 {
		return []byte(strings.Join(pos, " ")), nil
	}
	if c.App.StdinTTY {
		return nil, &UsageError{Msg: "nothing to read: pass text as an argument or pipe it in", Cmd: c.Meta.Name}
	}
	return io.ReadAll(c.Stdin)
}

// Printf writes formatted output to stdout.
func (c *Context) Printf(format string, a ...any) { fmt.Fprintf(c.Stdout, format, a...) }

// Println writes a line to stdout.
func (c *Context) Println(a ...any) { fmt.Fprintln(c.Stdout, a...) }
