package commands

import (
	"net/url"
	"sort"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("url", runURL) }

func runURL(c *core.Context) error {
	fs := c.Flags()
	decode := fs.Bool("d", false, "decode instead of encode")
	parse := fs.Bool("p", false, "take a URL apart: scheme, host, path, query")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	in, err := c.Input(pos)
	if err != nil {
		return err
	}
	text := strings.TrimRight(string(in), "\r\n")
	switch {
	case *parse:
		u, err := url.Parse(text)
		if err != nil {
			return err
		}
		row := func(k, v string) {
			if v != "" {
				c.Printf("%-9s %s\n", k, v)
			}
		}
		row("scheme", u.Scheme)
		row("user", u.User.String())
		row("host", u.Hostname())
		row("port", u.Port())
		row("path", u.Path)
		row("fragment", u.Fragment)
		q := u.Query()
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, v := range q[k] {
				c.Printf("%-9s %s = %s\n", "query", k, v)
			}
		}
	case *decode:
		out, err := url.QueryUnescape(text)
		if err != nil {
			return err
		}
		c.Println(out)
	default:
		c.Println(strings.ReplaceAll(url.QueryEscape(text), "+", "%20"))
	}
	return nil
}
