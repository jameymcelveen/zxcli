package commands

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("port", runPort) }

// Listener is one process listening on a TCP port.
type Listener struct {
	Port int
	PID  int
	Proc string
	User string
}

func runPort(c *core.Context) error {
	fs := c.Flags()
	kill := fs.Bool("k", false, "stop the process listening on the port")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	want := 0
	if len(pos) > 0 {
		if want, err = strconv.Atoi(pos[0]); err != nil || want < 1 || want > 65535 {
			return core.Usagef("port must be a number from 1 to 65535, got %q", pos[0])
		}
	}
	if *kill && want == 0 {
		return core.Usagef("say which port to free: zx port 3000 -k")
	}
	all, err := listListeners()
	if err != nil {
		return err
	}
	var found []Listener
	for _, l := range all {
		if want == 0 || l.Port == want {
			found = append(found, l)
		}
	}
	if len(found) == 0 {
		if want != 0 {
			c.Printf("nothing is listening on port %d\n", want)
		} else {
			c.Println("nothing is listening")
		}
		return nil
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Port < found[j].Port })
	if !*kill {
		userWidth := len("USER")
		for _, l := range found {
			userWidth = max(userWidth, len(l.User))
		}
		c.Printf("%-6s %-8s %-*s %s\n", "PORT", "PID", userWidth, "USER", "PROCESS")
		for _, l := range found {
			pid := "?"
			if l.PID > 0 {
				pid = strconv.Itoa(l.PID)
			}
			c.Printf("%-6d %-8s %-*s %s\n", l.Port, pid, userWidth, l.User, l.Proc)
		}
		return nil
	}
	for _, l := range found {
		if l.PID <= 0 {
			return fmt.Errorf("can't tell which process owns port %d (try again with sudo)", l.Port)
		}
		p, err := os.FindProcess(l.PID)
		if err == nil {
			err = p.Kill()
		}
		if err != nil {
			return fmt.Errorf("stopping %s (pid %d): %w", l.Proc, l.PID, err)
		}
		c.Printf("stopped %s (pid %d) on port %d\n", l.Proc, l.PID, l.Port)
	}
	return nil
}

func listListeners() ([]Listener, error) {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
		if err != nil {
			return nil, fmt.Errorf("netstat: %w", err)
		}
		tasks, _ := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
		return parseNetstat(string(out), string(tasks)), nil
	}
	if path, err := exec.LookPath("lsof"); err == nil {
		// lsof exits 1 when there are no matches, so the error is ignored.
		out, _ := exec.Command(path, "+c", "0", "-nP", "-iTCP", "-sTCP:LISTEN").Output()
		return parseLsof(string(out)), nil
	}
	if path, err := exec.LookPath("ss"); err == nil {
		out, err := exec.Command(path, "-ltnpH").Output()
		if err != nil {
			return nil, fmt.Errorf("ss: %w", err)
		}
		return parseSS(string(out)), nil
	}
	return nil, fmt.Errorf("need lsof or ss to look up ports, and neither is installed")
}

func portOf(addr string) int {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0
	}
	p, _ := strconv.Atoi(addr[i+1:])
	return p
}

func dedupe(in []Listener) []Listener {
	seen := map[[2]int]bool{}
	var out []Listener
	for _, l := range in {
		key := [2]int{l.Port, l.PID}
		if l.Port > 0 && !seen[key] {
			seen[key] = true
			out = append(out, l)
		}
	}
	return out
}

// parseLsof reads "lsof -nP -iTCP -sTCP:LISTEN" output.
func parseLsof(out string) []Listener {
	var res []Listener
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 || f[0] == "COMMAND" {
			continue
		}
		pid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		addr := f[len(f)-1]
		if strings.HasPrefix(addr, "(") {
			addr = f[len(f)-2]
		}
		res = append(res, Listener{
			Port: portOf(addr), PID: pid, User: f[2],
			Proc: strings.ReplaceAll(f[0], `\x20`, " "),
		})
	}
	return dedupe(res)
}

var ssProc = regexp.MustCompile(`\("([^"]+)",pid=(\d+)`)

// parseSS reads "ss -ltnpH" output.
func parseSS(out string) []Listener {
	var res []Listener
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		l := Listener{Port: portOf(f[3]), Proc: "?"}
		if m := ssProc.FindStringSubmatch(line); m != nil {
			l.Proc = m[1]
			l.PID, _ = strconv.Atoi(m[2])
		}
		res = append(res, l)
	}
	return dedupe(res)
}

// parseNetstat reads Windows "netstat -ano -p TCP" output. tasklist is the
// output of "tasklist /FO CSV /NH", used to turn PIDs into process names.
func parseNetstat(out, tasklist string) []Listener {
	names := map[int]string{}
	if rows, err := csv.NewReader(strings.NewReader(tasklist)).ReadAll(); err == nil {
		for _, r := range rows {
			if len(r) >= 2 {
				if pid, err := strconv.Atoi(r[1]); err == nil {
					names[pid] = r[0]
				}
			}
		}
	}
	var res []Listener
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "TCP" || f[3] != "LISTENING" {
			continue
		}
		pid, _ := strconv.Atoi(f[4])
		proc := names[pid]
		if proc == "" {
			proc = "?"
		}
		res = append(res, Listener{Port: portOf(f[1]), PID: pid, Proc: proc})
	}
	return dedupe(res)
}
