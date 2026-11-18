//go:build !windows

package core

import (
	"context"
	"os/exec"
)

// windowsShell is only called on Windows; this keeps other builds compiling.
func windowsShell(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "cmd", "/C", line)
}
