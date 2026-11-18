package commands

import (
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("paste", runPaste) }

// runPaste prints the clipboard, so it can be piped: zx paste | zx json
func runPaste(c *core.Context) error {
	if _, err := c.Parse(c.Flags()); err != nil {
		return err
	}
	text, err := core.Paste()
	if err != nil {
		return err
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	c.Printf("%s", text)
	return nil
}
