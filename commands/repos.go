package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("repos", runRepos) }

// RepoStatus is the state of one git repository.
type RepoStatus struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	Changed   int    `json:"changed"`
	Untracked int    `json:"untracked"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Upstream  string `json:"upstream,omitempty"`
	HasRemote bool   `json:"hasRemote"`
	Error     string `json:"error,omitempty"`
}

// Problems lists what needs attention, in plain words. Empty means clean.
func (r RepoStatus) Problems() []string {
	if r.Error != "" {
		return []string{r.Error}
	}
	var p []string
	if r.Changed > 0 {
		p = append(p, fmt.Sprintf("%d changed", r.Changed))
	}
	if r.Untracked > 0 {
		p = append(p, fmt.Sprintf("%d untracked", r.Untracked))
	}
	if r.Ahead > 0 {
		p = append(p, fmt.Sprintf("%d unpushed", r.Ahead))
	}
	if r.Behind > 0 {
		p = append(p, fmt.Sprintf("%d behind", r.Behind))
	}
	switch {
	case !r.HasRemote:
		p = append(p, "no remote")
	case r.Upstream == "" && r.Branch != "(detached)":
		p = append(p, "branch not pushed")
	}
	return p
}

func runRepos(c *core.Context) error {
	fs := c.Flags()
	all := fs.Bool("a", false, "list clean repos too")
	fetch := fs.Bool("fetch", false, "run git fetch in each repo first (slower, but \"behind\" is current)")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is not installed")
	}
	dir := ""
	switch {
	case len(pos) > 0:
		dir = pos[0]
	case c.App.Config.Setting("repos.dir", "") != "":
		dir = c.App.Config.Setting("repos.dir", "")
	case os.Getenv("ZX_REPOS") != "":
		dir = os.Getenv("ZX_REPOS")
	default:
		dir = "~/dev"
		if _, err := os.Stat(expandHome(dir)); err != nil {
			dir = "."
		}
	}
	dir = expandHome(dir)
	repos, err := ScanRepos(dir, *fetch)
	if err != nil {
		return err
	}

	var shown []RepoStatus
	dirty := 0
	for _, r := range repos {
		if len(r.Problems()) > 0 {
			dirty++
			shown = append(shown, r)
		} else if *all {
			shown = append(shown, r)
		}
	}
	if *asJSON {
		if shown == nil {
			shown = []RepoStatus{}
		}
		return c.JSON(shown)
	}
	nameW, branchW := len("REPO"), len("BRANCH")
	for _, r := range shown {
		nameW, branchW = max(nameW, len(r.Name)), max(branchW, len(r.Branch))
	}
	if len(shown) > 0 {
		c.Println(c.App.Dim(fmt.Sprintf("%-*s  %-*s  %s", nameW, "REPO", branchW, "BRANCH", "STATE")))
	}
	for _, r := range shown {
		state := strings.Join(r.Problems(), ", ")
		if state == "" {
			state = c.App.Dim("clean")
		}
		c.Printf("%s  %-*s  %s\n", c.App.Cyan(fmt.Sprintf("%-*s", nameW, r.Name)), branchW, r.Branch, state)
	}
	count := fmt.Sprintf("%d repos", len(repos))
	if len(repos) == 1 {
		count = "1 repo"
	}
	switch {
	case len(repos) == 0:
		c.Printf("no git repos found in %s\n", dir)
	case dirty == 0:
		c.Printf("%s in %s, all clean and pushed\n", count, dir)
	case dirty == 1:
		c.Printf("%s in %s, 1 needs attention\n", count, dir)
	default:
		c.Printf("%s in %s, %d need attention\n", count, dir, dirty)
	}
	return nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// ScanRepos reports on dir itself if it is a git repo, otherwise on every git
// repo directly inside it. Repos are checked in parallel.
func ScanRepos(dir string, fetch bool) ([]RepoStatus, error) {
	isRepo := func(p string) bool {
		_, err := os.Stat(filepath.Join(p, ".git"))
		return err == nil
	}
	var paths []string
	if isRepo(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		paths = []string{abs}
	} else {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if p := filepath.Join(dir, e.Name()); isRepo(p) {
				paths = append(paths, p)
			}
		}
	}
	out := make([]RepoStatus, len(paths))
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for i, p := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			out[i] = repoStatus(p, fetch)
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func git(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return string(out), err
}

func repoStatus(path string, fetch bool) RepoStatus {
	r := RepoStatus{Name: filepath.Base(path), Path: path}
	remotes, _ := git(path, 10*time.Second, "remote")
	r.HasRemote = strings.TrimSpace(remotes) != ""
	if fetch && r.HasRemote {
		if _, err := git(path, 30*time.Second, "fetch", "--quiet"); err != nil {
			r.Error = "fetch failed"
			return r
		}
	}
	out, err := git(path, 20*time.Second, "status", "--porcelain=v2", "--branch")
	if err != nil {
		r.Error = "git status failed"
		return r
	}
	parseStatusV2(out, &r)
	return r
}

// parseStatusV2 reads "git status --porcelain=v2 --branch" output into r.
func parseStatusV2(out string, r *RepoStatus) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			r.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.upstream "):
			r.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				n, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					r.Ahead = n
				} else {
					r.Behind = n
				}
			}
		case strings.HasPrefix(line, "? "):
			r.Untracked++
		case line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "! "):
			r.Changed++
		}
	}
}
