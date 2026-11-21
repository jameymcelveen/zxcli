//go:build !windows

package commands

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// keepAwake stops the machine from sleeping until release is called. On macOS
// it runs the system caffeinate tied to our own pid, so it cannot outlive us.
// Elsewhere it holds a systemd inhibitor lock open through a pipe that closes
// when we exit.
func keepAwake(why string, display bool) (release func(), how string, err error) {
	if runtime.GOOS == "darwin" {
		flags := "-ims"
		if display {
			flags = "-dims"
		}
		cmd := exec.Command("caffeinate", flags, "-w", strconv.Itoa(os.Getpid()))
		if err := cmd.Start(); err != nil {
			return nil, "", fmt.Errorf("can't start caffeinate: %w", err)
		}
		return func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }, "macOS caffeinate", nil
	}
	path, err := exec.LookPath("systemd-inhibit")
	if err != nil {
		return nil, "", fmt.Errorf("zx caffeinate needs systemd-inhibit on this system, and it is not installed")
	}
	what := "--what=sleep"
	if display {
		what = "--what=idle:sleep"
	}
	cmd := exec.Command(path, what, "--who=zx", "--why="+why, "--mode=block", "cat")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", err
	}
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("can't start systemd-inhibit: %w", err)
	}
	return func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }, "systemd-inhibit", nil
}

// rawMode makes the terminal hand over single keys as they are pressed.
func rawMode() (func(), error) {
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	out, err := get.Output()
	if err != nil {
		return nil, err
	}
	saved := strings.TrimSpace(string(out))
	set := exec.Command("stty", "-icanon", "-echo", "min", "1", "time", "0")
	set.Stdin = os.Stdin
	if err := set.Run(); err != nil {
		return nil, err
	}
	return func() {
		r := exec.Command("stty", saved)
		r.Stdin = os.Stdin
		_ = r.Run()
	}, nil
}

func termSize() (w, h int) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	if out, err := cmd.Output(); err == nil {
		if _, err := fmt.Sscanf(string(out), "%d %d", &h, &w); err == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	return 80, 24
}

// displayOff turns the screen off right away where the system lets us. On
// other systems the screen goes off by itself once zx lets go of it.
func displayOff() {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("pmset", "displaysleepnow").Run()
	}
}
