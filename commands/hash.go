package commands

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("hash", runHash) }

func runHash(c *core.Context) error {
	fs := c.Flags()
	algo := fs.String("a", "sha256", "`algorithm`: md5, sha1, sha256 or sha512")
	text := fs.String("s", "", "hash this `text` instead of a file")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	newHash, ok := map[string]func() hash.Hash{
		"md5": md5.New, "sha1": sha1.New, "sha256": sha256.New, "sha512": sha512.New,
	}[strings.ToLower(*algo)]
	if !ok {
		return core.Usagef("unknown algorithm %q: use md5, sha1, sha256 or sha512", *algo)
	}
	sum := func(r io.Reader) (string, error) {
		h := newHash()
		if _, err := io.Copy(h, r); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	switch {
	case *text != "":
		s, err := sum(strings.NewReader(*text))
		if err != nil {
			return err
		}
		c.Println(s)
	case len(pos) == 0:
		if c.App.StdinTTY {
			return core.Usagef("nothing to hash: name a file, use -s \"text\", or pipe data in")
		}
		s, err := sum(c.Stdin)
		if err != nil {
			return err
		}
		c.Println(s)
	default:
		for _, name := range pos {
			f, err := os.Open(name)
			if err != nil {
				return err
			}
			s, err := sum(f)
			f.Close()
			if err != nil {
				return err
			}
			c.Printf("%s  %s\n", s, name)
		}
	}
	return nil
}
