//go:build windows

package core

import (
	"context"
	"os/exec"
	"syscall"
)

// windowsShell runs one line through cmd.exe. The line is handed over
// verbatim: Go's usual argument escaping turns quotes into \" which cmd.exe
// does not understand, so a line such as  echo hi > "my file.txt"  would
// break. With /S, cmd strips the outer pair of quotes and runs the rest as
// typed.
func windowsShell(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + line + `"`}
	return cmd
}
