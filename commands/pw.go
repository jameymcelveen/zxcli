package commands

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("pw", runPw) }

const (
	pwLower   = "abcdefghijkmnopqrstuvwxyz" // no l
	pwUpper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"  // no I, O
	pwDigits  = "23456789"                  // no 0, 1
	pwSymbols = "!@#$%^&*-_=+?"
)

func runPw(c *core.Context) error {
	fs := c.Flags()
	length := fs.Int("l", 20, "password length")
	n := fs.Int("n", 1, "how many to generate")
	noSymbols := fs.Bool("no-symbols", false, "letters and digits only")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		v, err := strconv.Atoi(pos[0])
		if err != nil {
			return core.Usagef("length must be a number, got %q", pos[0])
		}
		*length = v
	}
	if *length < 4 || *length > 1024 {
		return core.Usagef("length must be between 4 and 1024")
	}
	if *n < 1 || *n > 10000 {
		return core.Usagef("count must be between 1 and 10000")
	}
	for i := 0; i < *n; i++ {
		p, err := NewPassword(*length, !*noSymbols)
		if err != nil {
			return err
		}
		c.Println(p)
	}
	return nil
}

// NewPassword returns a random password that contains at least one character
// from every class in use. Look-alike characters (0/O, 1/l/I) are left out.
func NewPassword(length int, symbols bool) (string, error) {
	classes := []string{pwLower, pwUpper, pwDigits}
	if symbols {
		classes = append(classes, pwSymbols)
	}
	all := strings.Join(classes, "")
	for {
		b := make([]byte, length)
		for i := range b {
			idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
			if err != nil {
				return "", err
			}
			b[i] = all[idx.Int64()]
		}
		ok := true
		for _, class := range classes {
			if !strings.ContainsAny(string(b), class) {
				ok = false
			}
		}
		if ok {
			return string(b), nil
		}
	}
}
