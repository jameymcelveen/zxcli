package commands

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

func TestGuidDefault(t *testing.T) {
	out := mustRun(t, "", "guid")
	if !uuidV4.MatchString(out) {
		t.Errorf("not a v4 UUID: %q", out)
	}
}

func TestGuidAlias(t *testing.T) {
	if out := mustRun(t, "", "uuid"); !uuidV4.MatchString(out) {
		t.Errorf("uuid alias printed %q", out)
	}
}

func TestGuidCountAndUniqueness(t *testing.T) {
	for _, args := range [][]string{{"guid", "50"}, {"guid", "-n", "50"}} {
		lines := strings.Split(mustRun(t, "", args...), "\n")
		if len(lines) != 50 {
			t.Fatalf("%v: got %d lines, want 50", args, len(lines))
		}
		seen := map[string]bool{}
		for _, l := range lines {
			if !uuidV4.MatchString(l) {
				t.Errorf("not a v4 UUID: %q", l)
			}
			if seen[l] {
				t.Errorf("duplicate UUID %s", l)
			}
			seen[l] = true
		}
	}
}

func TestGuidFormats(t *testing.T) {
	// Flags may come after the positional count.
	out := mustRun(t, "", "guid", "1", "-u", "-b")
	if !strings.HasPrefix(out, "{") || !strings.HasSuffix(out, "}") || out != strings.ToUpper(out) {
		t.Errorf("-u -b printed %q", out)
	}
	if bare := mustRun(t, "", "guid", "--bare"); len(bare) != 32 || strings.Contains(bare, "-") {
		t.Errorf("--bare printed %q", bare)
	}
}

func TestGuidV7IsTimeOrdered(t *testing.T) {
	if out := mustRun(t, "", "guid", "-7"); !uuidV7.MatchString(out) {
		t.Errorf("not a v7 UUID: %q", out)
	}
	early, _ := NewUUIDv7(time.UnixMilli(1_700_000_000_000))
	late, _ := NewUUIDv7(time.UnixMilli(1_700_000_000_001))
	if early >= late {
		t.Errorf("v7 UUIDs should sort by time: %s then %s", early, late)
	}
}

func TestGuidRejectsBadCount(t *testing.T) {
	for _, bad := range []string{"0", "-n=-3", "lots"} {
		if _, _, err := run(t, "", "guid", bad); err == nil {
			t.Errorf("guid %s should fail", bad)
		}
	}
}
