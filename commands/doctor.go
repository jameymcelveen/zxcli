package commands

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("doctor", runDoctor) }

// DefaultDoctorTools is checked when the "doctor.tools" setting is not set.
const DefaultDoctorTools = "git, gh, go, dotnet, node, npm, python3, docker, make"

// Check is one line of the doctor report.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Path   string `json:"path,omitempty"`
}

func runDoctor(c *core.Context) error {
	fs := c.Flags()
	strict := fs.Bool("strict", false, "exit with status 1 if anything is missing")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := c.Parse(fs); err != nil {
		return err
	}
	var checks []Check
	has := map[string]bool{}
	for _, name := range strings.Split(c.App.Config.Setting("doctor.tools", DefaultDoctorTools), ",") {
		if name = strings.TrimSpace(name); name != "" {
			ck := CheckTool(name)
			has[name] = ck.OK
			checks = append(checks, ck)
		}
	}
	if has["git"] {
		name, email := toolOutput("git", "config", "user.name"), toolOutput("git", "config", "user.email")
		ck := Check{Name: "git identity", OK: name != "" && email != "", Detail: strings.TrimSpace(name + " <" + email + ">")}
		if !ck.OK {
			ck.Detail = "set it with: git config --global user.name \"You\" and user.email"
		}
		checks = append(checks, ck)
	}
	if has["gh"] {
		ck := Check{Name: "gh signed in", Detail: "run: gh auth login"}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if exec.CommandContext(ctx, "gh", "auth", "status").Run() == nil {
			ck.OK, ck.Detail = true, ""
		}
		cancel()
		checks = append(checks, ck)
	}

	missing := 0
	for _, ck := range checks {
		if !ck.OK {
			missing++
		}
	}
	if *asJSON {
		if err := c.JSON(checks); err != nil {
			return err
		}
	} else {
		c.Printf("zx %s on %s/%s\n", core.Version, runtime.GOOS, runtime.GOARCH)
		width, detailWidth := 0, 0
		for _, ck := range checks {
			width = max(width, len(ck.Name))
			if ck.Path != "" {
				detailWidth = max(detailWidth, len(ck.Detail))
			}
		}
		for _, ck := range checks {
			mark := c.App.Cyan("ok     ")
			if !ck.OK {
				mark = c.App.Bold("missing")
			}
			line := strings.TrimRight(mark+"  "+padRight(ck.Name, width)+"  "+ck.Detail, " ")
			if ck.Path != "" {
				line = mark + "  " + padRight(ck.Name, width) + "  " + padRight(ck.Detail, detailWidth) + "  " + c.App.Dim(ck.Path)
			}
			c.Println(line)
		}
		if missing == 0 {
			c.Println("everything checks out")
		} else {
			c.Printf("%d of %d checks need attention\n", missing, len(checks))
		}
	}
	if *strict && missing > 0 {
		return &core.ExitError{Code: 1}
	}
	return nil
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

var versionNumber = regexp.MustCompile(`\d+\.\d+(\.\d+)?[\w.+-]*`)

// CheckTool looks for a program on PATH and asks it for its version.
func CheckTool(name string) Check {
	path, err := exec.LookPath(name)
	if err != nil {
		return Check{Name: name, Detail: "not installed"}
	}
	args := []string{"--version"}
	if name == "go" {
		args = []string{"version"}
	}
	out := toolOutput(path, args...)
	version := versionNumber.FindString(out)
	if version == "" {
		version = "installed"
	}
	return Check{Name: name, OK: true, Detail: version, Path: path}
}

// toolOutput runs a command and returns its trimmed output, or "" on failure.
func toolOutput(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
