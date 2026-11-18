// Package commands holds the built-in zx commands, one file per command.
//
// To add a command: create <name>.go here with an init() that calls
// core.Register, and add an entry for it to zxcli.json. "zx new <name>"
// does both for you.
package commands

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("guid", runGuid) }

func runGuid(c *core.Context) error {
	fs := c.Flags()
	n := fs.Int("n", 1, "how many to generate")
	v7 := fs.Bool("7", false, "time-ordered UUIDv7 instead of random v4")
	upper := fs.Bool("u", false, "uppercase")
	bare := fs.Bool("bare", false, "no dashes")
	braces := fs.Bool("b", false, "wrap in {braces}")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		v, err := strconv.Atoi(pos[0])
		if err != nil {
			return core.Usagef("count must be a number, got %q", pos[0])
		}
		*n = v
	}
	if *n < 1 || *n > 100000 {
		return core.Usagef("count must be between 1 and 100000")
	}
	for i := 0; i < *n; i++ {
		var id string
		if *v7 {
			id, err = NewUUIDv7(time.Now())
		} else {
			id, err = NewUUIDv4()
		}
		if err != nil {
			return err
		}
		if *bare {
			id = strings.ReplaceAll(id, "-", "")
		}
		if *upper {
			id = strings.ToUpper(id)
		}
		if *braces {
			id = "{" + id + "}"
		}
		c.Println(id)
	}
	return nil
}

// NewUUIDv4 returns a random (version 4) UUID.
func NewUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return formatUUID(b), nil
}

// NewUUIDv7 returns a time-ordered (version 7) UUID. These sort by creation
// time, which makes them friendlier database keys than v4.
func NewUUIDv7(t time.Time) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[6:]); err != nil {
		return "", err
	}
	ms := uint64(t.UnixMilli())
	b[0], b[1], b[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	b[3], b[4], b[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	return formatUUID(b), nil
}

func formatUUID(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
