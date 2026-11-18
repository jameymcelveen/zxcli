package commands

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("doctor", runDoctor) }

// DefaultDoctorTools is checked when the user config has neither a "doctor"
// list nor a "doctor.tools" setting.
const DefaultDoctorTools = "git, gh, go, dotnet, node, npm, python3, docker, make"

// Check is one line of the doctor report.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, missing, too old, failed or skipped
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Path   string `json:"path,omitempty"`
	Source string `json:"source"` // machine or project
	Fix    string `json:"fix,omitempty"`
}

func runDoctor(c *core.Context) error {
	fs := c.Flags()
	strict := fs.Bool("strict", false, "exit with status 1 if anything needs attention")
	asJSON := fs.Bool("json", false, "print JSON")
	fix := fs.Bool("fix", false, "install what is missing, after showing the commands and asking")
	yes := fs.Bool("yes", false, "with --fix: do not ask before running the commands")
	trust := fs.Bool("trust", false, "approve this project's custom checks so they may run")
	if _, err := c.Parse(fs); err != nil {
		return err
	}
	cfg := c.App.Config
	plan := DoctorPlan(cfg)

	// A project's custom checks are shell commands written by whoever wrote
	// the repo. They run only after the user approves them with --trust, and
	// need approving again whenever they change.
	trustFile, runs := "", projectRuns(plan)
	if cfg.UserPath != "" {
		trustFile = filepath.Join(filepath.Dir(cfg.UserPath), "trust.json")
	}
	approved := len(runs) == 0 || isTrusted(trustFile, cfg.ProjectPath, runs)
	if *trust && len(runs) > 0 && !approved {
		fmt.Fprintf(c.Stderr, "approved %d custom checks from %s:\n", len(runs), cfg.ProjectPath)
		for _, r := range runs {
			fmt.Fprintf(c.Stderr, "  %s\n", r)
		}
		if err := saveTrust(trustFile, cfg.ProjectPath, runs); err != nil {
			return fmt.Errorf("can't save the approval: %w", err)
		}
		approved = true
	}

	checks := runDoctorPlan(plan, approved)
	if *fix {
		ran, err := applyFixes(c, checks, *yes)
		if err != nil {
			return err
		}
		if ran {
			checks = runDoctorPlan(plan, approved)
		}
	}

	attention := 0
	for _, ck := range checks {
		if !ck.OK {
			attention++
		}
	}
	if *asJSON {
		if err := c.JSON(checks); err != nil {
			return err
		}
	} else {
		printDoctor(c, cfg, checks, attention, *fix)
	}
	if *strict && attention > 0 {
		return &core.ExitError{Code: 1}
	}
	return nil
}

// DoctorPlan lists what to check: this machine's list, then the project's. A
// project entry for a tool replaces the machine's entry for the same tool.
func DoctorPlan(cfg core.Config) []core.DoctorCheck {
	machine := cfg.UserDoctor
	if machine == nil {
		for _, name := range strings.Split(cfg.Setting("doctor.tools", DefaultDoctorTools), ",") {
			if name = strings.TrimSpace(name); name != "" {
				machine = append(machine, core.DoctorCheck{Tool: name})
			}
		}
	}
	inProject := map[string]bool{}
	for _, d := range cfg.ProjectDoctor {
		if d.Tool != "" {
			inProject[d.Tool] = true
		}
	}
	var plan []core.DoctorCheck
	for _, d := range machine {
		if d.Tool == "" || !inProject[d.Tool] {
			plan = append(plan, d)
		}
	}
	return append(plan, cfg.ProjectDoctor...)
}

func runDoctorPlan(plan []core.DoctorCheck, approved bool) []Check {
	var checks []Check
	found := map[string]bool{}
	for _, d := range plan {
		ck := evalCheck(d, approved)
		if d.Tool != "" && !d.Project {
			found[d.Tool] = ck.Status != "missing"
		}
		checks = append(checks, ck)
	}
	// Two extra checks that ride along with git and gh on the machine list.
	var extra []Check
	if found["git"] {
		name, email := toolOutput("git", "config", "user.name"), toolOutput("git", "config", "user.email")
		ck := Check{Name: "git identity", Source: "machine", Status: "ok", OK: true, Detail: name + " <" + email + ">"}
		if name == "" || email == "" {
			ck.Status, ck.OK = "missing", false
			ck.Detail = "set it with: git config --global user.name \"You\" (and user.email)"
		}
		extra = append(extra, ck)
	}
	if found["gh"] {
		ck := Check{Name: "gh signed in", Source: "machine", Status: "ok", OK: true}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if exec.CommandContext(ctx, "gh", "auth", "status").Run() != nil {
			ck.Status, ck.OK, ck.Detail = "missing", false, "run: gh auth login"
		}
		cancel()
		extra = append(extra, ck)
	}
	// Keep machine checks together, ahead of the project's.
	split := len(checks)
	for i, ck := range checks {
		if ck.Source == "project" {
			split = i
			break
		}
	}
	out := append([]Check{}, checks[:split]...)
	out = append(out, extra...)
	return append(out, checks[split:]...)
}

func evalCheck(d core.DoctorCheck, approved bool) Check {
	ck := Check{Name: d.Label(), Source: "machine"}
	if d.Project {
		ck.Source = "project"
	}
	if d.Tool == "" {
		if d.Project && !approved {
			ck.Status, ck.Detail = "skipped", "not approved yet. Review it, then run: zx doctor --trust"
			return ck
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := core.ShellCommandContext(ctx, d.Run, d.Dir).CombinedOutput()
		if err == nil {
			ck.Status, ck.OK = "ok", true
			return ck
		}
		ck.Status, ck.Detail = "failed", d.Run
		if line := firstLine(string(out)); line != "" {
			ck.Detail = line
		}
		ck.Fix = d.Install.For(runtime.GOOS)
		return ck
	}

	builtinInstall, builtinUpgrade := BuiltinFix(d.Tool, runtime.GOOS)
	install := firstNonEmpty(d.Install.For(runtime.GOOS), builtinInstall)
	path, err := exec.LookPath(d.Tool)
	if err != nil {
		ck.Status, ck.Detail, ck.Fix = "missing", "not installed", install
		if d.Min != "" {
			ck.Detail = "not installed (needs " + d.Min + "+)"
		}
		return ck
	}
	ck.Path = path
	args := []string{"--version"}
	if d.Tool == "go" {
		args = []string{"version"}
	}
	version := versionNumber.FindString(toolOutput(path, args...))
	ck.Status, ck.OK, ck.Detail = "ok", true, firstNonEmpty(version, "installed")
	if d.Min != "" {
		switch {
		case version == "":
			ck.Detail = "installed (could not read its version to compare with " + d.Min + ")"
		case !VersionAtLeast(version, d.Min):
			ck.Status, ck.OK = "too old", false
			ck.Detail = version + " (needs " + d.Min + "+)"
			ck.Fix = firstNonEmpty(d.Upgrade.For(runtime.GOOS), d.Install.For(runtime.GOOS), builtinUpgrade)
		}
	}
	return ck
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 100 {
		s = s[:100] + "..."
	}
	return s
}

var (
	versionNumber = regexp.MustCompile(`\d+\.\d+(\.\d+)?[\w.+-]*`)
	digits        = regexp.MustCompile(`\d+`)
)

// VersionAtLeast reports whether have is the same as or newer than want.
// Only the parts that want spells out are compared, so 22.3.1 satisfies "22".
func VersionAtLeast(have, want string) bool {
	h, w := digits.FindAllString(have, -1), digits.FindAllString(want, -1)
	for i := range w {
		a, b := 0, 0
		if i < len(h) {
			a, _ = strconv.Atoi(h[i])
		}
		b, _ = strconv.Atoi(w[i])
		if a != b {
			return a > b
		}
	}
	return true
}

var doctorLookPath = exec.LookPath

// BuiltinFix returns the usual install and upgrade commands for a well-known
// tool, using the package manager that is actually present: Homebrew on
// macOS, winget on Windows, apt on Linux. Both are "" when zx has no
// suggestion.
func BuiltinFix(tool, goos string) (install, upgrade string) {
	has := func(name string) bool { _, err := doctorLookPath(name); return err == nil }
	switch goos {
	case "darwin":
		if tool == "make" {
			return "xcode-select --install", ""
		}
		pkg := map[string]string{
			"git": "git", "gh": "gh", "go": "go", "node": "node", "npm": "node", "python3": "python",
			"dotnet": "--cask dotnet-sdk", "docker": "--cask docker",
		}[tool]
		if pkg != "" && has("brew") {
			return "brew install " + pkg, "brew upgrade " + pkg
		}
	case "windows":
		id := map[string]string{
			"git": "Git.Git", "gh": "GitHub.cli", "go": "GoLang.Go", "node": "OpenJS.NodeJS.LTS", "npm": "OpenJS.NodeJS.LTS",
			"dotnet": "Microsoft.DotNet.SDK.10", "docker": "Docker.DockerDesktop",
		}[tool]
		if id != "" && has("winget") {
			return "winget install -e --id " + id, "winget upgrade -e --id " + id
		}
	case "linux":
		pkg := map[string]string{
			"git": "git", "gh": "gh", "go": "golang-go", "node": "nodejs", "npm": "npm", "python3": "python3",
			"make": "make", "docker": "docker.io",
		}[tool]
		if pkg != "" && has("apt-get") {
			cmd := "sudo apt-get install -y " + pkg
			return cmd, cmd
		}
	}
	return "", ""
}

// applyFixes shows every fix command, asks once, and runs them. It reports
// whether anything was run.
func applyFixes(c *core.Context, checks []Check, yes bool) (bool, error) {
	var fixes []Check
	seen := map[string]bool{}
	for _, ck := range checks {
		if !ck.OK && ck.Fix != "" && !seen[ck.Fix] {
			seen[ck.Fix] = true
			fixes = append(fixes, ck)
		}
	}
	if len(fixes) == 0 {
		fmt.Fprintln(c.Stderr, "nothing to fix automatically")
		return false, nil
	}
	fmt.Fprintf(c.Stderr, "zx doctor --fix will run:\n")
	for _, ck := range fixes {
		fmt.Fprintf(c.Stderr, "  %s   %s\n", ck.Fix, c.App.Dim("("+ck.Name+", from this "+ck.Source+")"))
	}
	if !yes {
		fmt.Fprintf(c.Stderr, "Run %s? [y/N] ", plural(len(fixes), "this command", "these commands"))
		answer, _ := bufio.NewReader(c.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(c.Stderr, "nothing was run")
			return false, nil
		}
	}
	for _, ck := range fixes {
		fmt.Fprintf(c.Stderr, "\n%s %s\n", c.App.Bold("running"), ck.Fix)
		if err := core.RunCommand(core.ShellCommand(ck.Fix, ""), os.Stdin, c.Stderr, c.Stderr); err != nil {
			fmt.Fprintf(c.Stderr, "that command failed: %v\n", err)
		}
	}
	fmt.Fprintln(c.Stderr)
	return true, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func printDoctor(c *core.Context, cfg core.Config, checks []Check, attention int, fixed bool) {
	c.Printf("zx %s on %s/%s\n", core.Version, runtime.GOOS, runtime.GOARCH)
	nameW, detailW := 0, 0
	for _, ck := range checks {
		nameW = max(nameW, len(ck.Name))
		if ck.Path != "" {
			detailW = max(detailW, len(ck.Detail))
		}
	}
	section := ""
	for _, ck := range checks {
		if ck.Source != section {
			section = ck.Source
			title, where := "This machine", cfg.UserPath
			if section == "project" {
				title, where = "This project", cfg.ProjectPath
			}
			c.Printf("\n%s  %s\n", c.App.Bold(title), c.App.Dim(where))
		}
		mark := padRight(ck.Status, 7)
		if ck.OK {
			mark = c.App.Cyan(mark)
		} else {
			mark = c.App.Bold(mark)
		}
		line := mark + "  " + padRight(ck.Name, nameW)
		if ck.Path != "" {
			line += "  " + padRight(ck.Detail, detailW) + "  " + c.App.Dim(ck.Path)
		} else if ck.Detail != "" {
			line += "  " + ck.Detail
		}
		c.Println(line)
	}
	c.Println()
	if attention == 0 {
		c.Println("everything checks out")
		return
	}
	c.Printf("%d of %d checks need attention\n", attention, len(checks))
	var fixes []string
	seen := map[string]bool{}
	for _, ck := range checks {
		if !ck.OK && ck.Fix != "" && !seen[ck.Fix] {
			seen[ck.Fix] = true
			fixes = append(fixes, ck.Fix)
		}
	}
	if len(fixes) > 0 && !fixed {
		c.Printf("\n%s  %s\n", c.App.Bold("Fixes"), c.App.Dim("run them all with: zx doctor --fix"))
		for _, f := range fixes {
			c.Printf("  %s\n", f)
		}
	}
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// CheckTool looks for a program on PATH and asks it for its version.
func CheckTool(name string) Check {
	return evalCheck(core.DoctorCheck{Tool: name}, true)
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

// projectRuns returns the shell lines of the project's custom checks.
func projectRuns(plan []core.DoctorCheck) []string {
	var runs []string
	for _, d := range plan {
		if d.Project && d.Run != "" {
			runs = append(runs, d.Run)
		}
	}
	return runs
}

func trustHash(runs []string) string {
	sum := sha256.Sum256([]byte(strings.Join(runs, "\x00")))
	return hex.EncodeToString(sum[:])
}

func readTrust(file string) map[string]string {
	trusted := map[string]string{}
	if data, err := os.ReadFile(file); err == nil {
		json.Unmarshal(data, &trusted)
	}
	return trusted
}

// isTrusted reports whether the user approved exactly these commands for
// this project file.
func isTrusted(file, project string, runs []string) bool {
	return file != "" && project != "" && readTrust(file)[project] == trustHash(runs)
}

func saveTrust(file, project string, runs []string) error {
	if file == "" || project == "" {
		return errors.New("no config folder to store it in")
	}
	trusted := readTrust(file)
	trusted[project] = trustHash(runs)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(trusted); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, buf.Bytes(), 0o644)
}
