package commands

import "github.com/jameymcelveen/zxcli/core"

func init() { core.Register("help", runHelp) }

func runHelp(c *core.Context) error {
	pos, err := c.Parse(c.Flags())
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		c.App.Help(c.Stdout)
		return nil
	}
	return c.App.HelpFor(pos[0], c.Stdout)
}
