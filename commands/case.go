package commands

import (
	"strings"
	"unicode"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("case", runCase) }

var caseStyles = []string{"camel", "pascal", "snake", "kebab", "const", "title", "lower", "upper"}

func runCase(c *core.Context) error {
	pos, err := c.Parse(c.Flags())
	if err != nil {
		return err
	}
	style := ""
	if len(pos) > 1 {
		for _, s := range caseStyles {
			if pos[0] == s {
				style, pos = s, pos[1:]
			}
		}
	}
	in, err := c.Input(pos)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(string(in))
	if style != "" {
		c.Println(ConvertCase(style, text))
		return nil
	}
	for _, s := range caseStyles {
		c.Printf("%-7s %s\n", s, ConvertCase(s, text))
	}
	return nil
}

// SplitWords breaks text into lowercase words. It splits on anything that is
// not a letter or digit, and on camelCase boundaries ("XMLHttpRequest" gives
// xml, http, request).
func SplitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	r := []rune(s)
	for i, ch := range r {
		if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(ch) && len(cur) > 0 {
			prev := r[i-1]
			nextLower := i+1 < len(r) && unicode.IsLower(r[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, ch)
	}
	flush()
	return words
}

func capitalize(w string) string {
	r := []rune(w)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// ConvertCase rewrites text in the given style. Unknown styles return the
// text unchanged.
func ConvertCase(style, text string) string {
	words := SplitWords(text)
	if len(words) == 0 {
		return ""
	}
	titled := make([]string, len(words))
	for i, w := range words {
		titled[i] = capitalize(w)
	}
	switch style {
	case "camel":
		return words[0] + strings.Join(titled[1:], "")
	case "pascal":
		return strings.Join(titled, "")
	case "snake":
		return strings.Join(words, "_")
	case "kebab":
		return strings.Join(words, "-")
	case "const":
		return strings.ToUpper(strings.Join(words, "_"))
	case "title":
		return strings.Join(titled, " ")
	case "lower":
		return strings.Join(words, " ")
	case "upper":
		return strings.ToUpper(strings.Join(words, " "))
	}
	return text
}
