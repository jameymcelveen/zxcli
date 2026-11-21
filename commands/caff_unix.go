//go:build !windows

package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
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

// goFullscreen puts the terminal window zx is running in into macOS full
// screen and returns a func that puts it back. It only knows Terminal and
// iTerm2, and needs that app to be allowed under Accessibility. A window that
// is already fullscreen is left alone.
func goFullscreen() (func(), error) {
	noop := func() {}
	if runtime.GOOS != "darwin" {
		return noop, nil
	}
	app := map[string]string{"Apple_Terminal": "Terminal", "iTerm.app": "iTerm2"}[os.Getenv("TERM_PROGRAM")]
	if app == "" {
		return noop, nil
	}
	attr := `tell application "System Events" to tell process "` + app + `" to `
	was, err := osa(attr + `get value of attribute "AXFullScreen" of window 1`)
	if err != nil {
		return noop, err
	}
	if was == "true" {
		return noop, nil
	}
	if _, err := osa(attr + `set value of attribute "AXFullScreen" of window 1 to true`); err != nil {
		return noop, err
	}
	return func() { _, _ = osa(attr + `set value of attribute "AXFullScreen" of window 1 to false`) }, nil
}

func osa(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
