package commands

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("clip", runClip) }

// runClip copies its arguments or stdin to the clipboard. It is pbcopy that
// works the same on macOS, Windows and Linux.
func runClip(c *core.Context) error {
	fs := c.Flags()
	keep := fs.Bool("k", false, "keep the trailing newline")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	in, err := c.Input(pos)
	if err != nil {
		return err
	}
	text := string(in)
	if !*keep {
		text = strings.TrimRight(text, "\r\n")
	}
	if err := core.Copy(text); err != nil {
		return err
	}
	fmt.Fprintf(c.Stderr, "copied %d characters\n", utf8.RuneCountInString(text))
	return nil
}
