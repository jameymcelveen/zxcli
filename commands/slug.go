package commands

import (
	"strings"
	"unicode"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("slug", runSlug) }

func runSlug(c *core.Context) error {
	pos, err := c.Parse(c.Flags())
	if err != nil {
		return err
	}
	in, err := c.Input(pos)
	if err != nil {
		return err
	}
	c.Println(Slugify(string(in)))
	return nil
}

// Slugify lowercases text and joins its letter and digit runs with dashes.
// Apostrophes are dropped so "don't" becomes "dont".
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		case r == '\'' || r == '’':
			// skip
		default:
			dash = true
		}
	}
	return b.String()
}
