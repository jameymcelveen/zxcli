package commands

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("jwt", runJWT) }

func runJWT(c *core.Context) error {
	pos, err := c.Parse(c.Flags())
	if err != nil {
		return err
	}
	in, err := c.Input(pos)
	if err != nil {
		return err
	}
	header, payload, err := DecodeJWT(string(in))
	if err != nil {
		return err
	}
	c.Println(c.App.Bold("header"))
	c.Println(string(header))
	c.Println(c.App.Bold("payload"))
	c.Println(string(payload))

	var claims map[string]any
	if json.Unmarshal(payload, &claims) == nil {
		now := time.Now()
		for _, key := range []string{"iat", "nbf", "exp"} {
			secs, ok := claims[key].(float64)
			if !ok {
				continue
			}
			t := time.Unix(int64(secs), 0)
			note := humanDuration(now.Sub(t)) + " ago"
			if t.After(now) {
				note = "in " + humanDuration(t.Sub(now))
			}
			if key == "exp" && t.Before(now) {
				note = "EXPIRED " + note
			}
			c.Printf("%-3s  %s  (%s)\n", key, t.Format("2006-01-02 15:04:05 MST"), note)
		}
	}
	fmt.Fprintln(c.Stderr, c.App.Dim("signature not verified"))
	return nil
}

// DecodeJWT returns the pretty-printed header and payload of a token. It does
// not check the signature.
func DecodeJWT(token string) (header, payload []byte, err error) {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, errors.New("not a JWT: expected three parts separated by dots")
	}
	decode := func(part, name string) ([]byte, error) {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(part, "="))
		if err != nil {
			return nil, fmt.Errorf("JWT %s is not valid base64url", name)
		}
		return FormatJSON(raw, false, 2)
	}
	if header, err = decode(parts[0], "header"); err != nil {
		return nil, nil, err
	}
	if payload, err = decode(parts[1], "payload"); err != nil {
		return nil, nil, err
	}
	return header, payload, nil
}
