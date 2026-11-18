package commands

import (
	"runtime"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("version", runVersion) }

func runVersion(c *core.Context) error {
	if _, err := c.Parse(c.Flags()); err != nil {
		return err
	}
	c.Printf("zx %s (commit %s, built %s, %s %s/%s)\n",
		core.Version, core.Commit, core.Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return nil
}
