package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("now", runNow) }

func runNow(c *core.Context) error {
	fs := c.Flags()
	field := fs.String("f", "", "print one `field` only: local, utc, iso, epoch or millis")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	t := time.Now()
	if len(pos) > 0 {
		if t, err = ParseWhen(strings.Join(pos, " "), time.Local); err != nil {
			return core.Usagef("%v", err)
		}
	}
	rows := whenRows(t, time.Local)
	if *asJSON {
		return c.JSON(map[string]any{
			"local": rows[0][1], "utc": rows[1][1], "iso": rows[2][1],
			"epoch": t.Unix(), "millis": t.UnixMilli(),
		})
	}
	if *field != "" {
		for _, r := range rows {
			if r[0] == *field {
				c.Println(r[1])
				return nil
			}
		}
		return core.Usagef("unknown field %q: use local, utc, iso, epoch or millis", *field)
	}
	for _, r := range rows {
		c.Printf("%-7s %s\n", r[0], r[1])
	}
	return nil
}

func whenRows(t time.Time, loc *time.Location) [][2]string {
	return [][2]string{
		{"local", t.In(loc).Format("2006-01-02 15:04:05 MST")},
		{"utc", t.UTC().Format("2006-01-02 15:04:05 MST")},
		{"iso", t.UTC().Format(time.RFC3339)},
		{"epoch", strconv.FormatInt(t.Unix(), 10)},
		{"millis", strconv.FormatInt(t.UnixMilli(), 10)},
	}
}

var whenLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"01/02/2006 15:04:05",
	"01/02/2006",
	time.RFC1123Z,
	time.RFC1123,
}

// ParseWhen reads an epoch (seconds, milliseconds, microseconds or
// nanoseconds, told apart by size) or a date in a handful of common layouts.
// Dates without a zone are read in loc.
func ParseWhen(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		abs := n
		if abs < 0 {
			abs = -abs
		}
		switch {
		case abs < 1e11:
			return time.Unix(n, 0), nil
		case abs < 1e14:
			return time.UnixMilli(n), nil
		case abs < 1e17:
			return time.UnixMicro(n), nil
		default:
			return time.Unix(0, n), nil
		}
	}
	for _, layout := range whenLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("can't read %q as an epoch or a date (try 1700000000 or 2026-11-18T10:00:00Z)", s)
}

// humanDuration renders a duration the way a person would say it: "3d 4h".
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
