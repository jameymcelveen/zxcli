package commands

import (
	"bytes"
	"encoding/base64"
	"errors"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("b64", runB64) }

func runB64(c *core.Context) error {
	fs := c.Flags()
	decode := fs.Bool("d", false, "decode instead of encode")
	urlSafe := fs.Bool("u", false, "use the URL-safe alphabet (- and _) when encoding")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	in, err := c.Input(pos)
	if err != nil {
		return err
	}
	if *decode {
		out, err := DecodeBase64(in)
		if err != nil {
			return err
		}
		_, err = c.Stdout.Write(out)
		if err == nil && len(pos) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
			c.Println()
		}
		return err
	}
	enc := base64.StdEncoding
	if *urlSafe {
		enc = base64.URLEncoding
	}
	c.Println(enc.EncodeToString(in))
	return nil
}

// DecodeBase64 accepts standard or URL-safe base64, padded or not, with any
// surrounding whitespace.
func DecodeBase64(in []byte) ([]byte, error) {
	s := string(bytes.Join(bytes.Fields(in), nil))
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if out, err := enc.DecodeString(s); err == nil {
			return out, nil
		}
	}
	return nil, errors.New("that is not valid base64")
}
