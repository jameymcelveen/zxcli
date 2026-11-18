package core

import (
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
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		line := s.Run
		for _, a := range args {
			if strings.ContainsAny(a, " \t&|<>^") {
				a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
			}
			line += " " + a
		}
		cmd = exec.Command("cmd", "/C", line)
	} else {
		run := s.Run
		if !positional.MatchString(run) {
			run += ` "$@"`
		}
		cmd = exec.Command("sh", append([]string{"-c", run, "zx"}, args...)...)
	}
	cmd.Dir = s.Dir
	return cmd
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
