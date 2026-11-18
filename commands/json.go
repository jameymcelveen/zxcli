package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("json", runJSON) }

func runJSON(c *core.Context) error {
	fs := c.Flags()
	minify := fs.Bool("m", false, "minify instead of pretty-print")
	indent := fs.Int("indent", 2, "spaces per indent level")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	var in []byte
	if len(pos) == 1 {
		in, err = os.ReadFile(pos[0])
	} else if len(pos) == 0 {
		in, err = c.Input(nil)
	} else {
		return core.Usagef("give one file, or pipe JSON in")
	}
	if err != nil {
		return err
	}
	out, err := FormatJSON(in, *minify, *indent)
	if err != nil {
		return err
	}
	c.Println(string(out))
	return nil
}

// FormatJSON validates JSON and reformats it. Key order is preserved.
func FormatJSON(in []byte, minify bool, indent int) ([]byte, error) {
	in = bytes.TrimSpace(in)
	var buf bytes.Buffer
	var err error
	if minify {
		err = json.Compact(&buf, in)
	} else {
		err = json.Indent(&buf, in, "", strings.Repeat(" ", indent))
	}
	if err != nil {
		var se *json.SyntaxError
		if errors.As(err, &se) {
			line := 1 + bytes.Count(in[:min(int(se.Offset), len(in))], []byte("\n"))
			return nil, fmt.Errorf("invalid JSON near line %d: %v", line, err)
		}
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return buf.Bytes(), nil
}
