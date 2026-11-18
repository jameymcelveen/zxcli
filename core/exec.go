package core

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

var positional = regexp.MustCompile(`\$[@*1-9]|\$\{[@*1-9]`)

// shellCmd builds the process for a script command. Extra arguments are
// passed along: appended to the line, or wherever the script uses "$@"/$1.
func shellCmd(s Script, args []string) *exec.Cmd {
	return shellCmdContext(context.Background(), s, args)
}

func shellCmdContext(ctx context.Context, s Script, args []string) *exec.Cmd {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		line := s.Run
		for _, a := range args {
			if strings.ContainsAny(a, " \t&|<>^") {
				a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
			}
			line += " " + a
		}
		cmd = windowsShell(ctx, line)
	} else {
		run := s.Run
		if !positional.MatchString(run) {
			run += ` "$@"`
		}
		cmd = exec.CommandContext(ctx, "sh", append([]string{"-c", run, "zx"}, args...)...)
	}
	cmd.Dir = s.Dir
	return cmd
}

// ShellCommand builds a process that runs one shell line in dir ("" means the
// current directory): sh -c on macOS and Linux, cmd /C on Windows.
func ShellCommand(line, dir string) *exec.Cmd {
	return shellCmd(Script{Run: line, Dir: dir}, nil)
}

// ShellCommandContext is ShellCommand with a context, for timeouts.
func ShellCommandContext(ctx context.Context, line, dir string) *exec.Cmd {
	return shellCmdContext(ctx, Script{Run: line, Dir: dir}, nil)
}

// RunCommand runs cmd with the given streams. A non-zero exit comes back as
// an *ExitError.
func RunCommand(cmd *exec.Cmd, stdin io.Reader, stdout, stderr io.Writer) error {
	return runCmd(cmd, stdin, stdout, stderr)
}

func runCmd(cmd *exec.Cmd, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.ExitCode()}
	}
	return err
}

// ExternalPlugins lists the zx-<name> programs on PATH, without the prefix.
func ExternalPlugins() []string {
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "zx-") || e.IsDir() {
				continue
			}
			if runtime.GOOS == "windows" {
				ext := strings.ToLower(filepath.Ext(name))
				if ext != ".exe" && ext != ".cmd" && ext != ".bat" {
					continue
				}
				name = strings.TrimSuffix(name, filepath.Ext(name))
			} else if info, err := e.Info(); err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			if n := strings.TrimPrefix(name, "zx-"); n != "" {
				seen[n] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Copy puts text on the system clipboard.
func Copy(text string) error {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"clip"}}
	default:
		candidates = [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
			{"clip.exe"}, // WSL
		}
	}
	for _, c := range candidates {
		path, err := exec.LookPath(c[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, c[1:]...)
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	return errors.New("no clipboard tool found (install wl-clipboard, xclip or xsel)")
}

// Paste returns the text on the system clipboard.
func Paste() (string, error) {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"pbpaste"}}
	case "windows":
		candidates = [][]string{{"powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw"}}
	default:
		candidates = [][]string{
			{"wl-paste", "--no-newline"},
			{"xclip", "-selection", "clipboard", "-o"},
			{"xsel", "--clipboard", "--output"},
			{"powershell.exe", "-NoProfile", "-Command", "Get-Clipboard -Raw"}, // WSL
		}
	}
	for _, c := range candidates {
		path, err := exec.LookPath(c[0])
		if err != nil {
			continue
		}
		out, err := exec.Command(path, c[1:]...).Output()
		if err != nil {
			return "", err
		}
		return strings.ReplaceAll(string(out), "\r\n", "\n"), nil
	}
	return "", errors.New("no clipboard tool found (install wl-clipboard, xclip or xsel)")
}
