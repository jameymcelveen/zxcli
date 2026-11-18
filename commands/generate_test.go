package commands

import (
	"strings"
	"testing"
)

func TestPwDefaults(t *testing.T) {
	out := mustRun(t, "", "pw")
	if len(out) != 20 {
		t.Errorf("default length is %d, want 20", len(out))
	}
	for name, class := range map[string]string{"lower": pwLower, "upper": pwUpper, "digit": pwDigits, "symbol": pwSymbols} {
		if !strings.ContainsAny(out, class) {
			t.Errorf("password %q has no %s character", out, name)
		}
	}
	if strings.ContainsAny(out, "0O1lI") {
		t.Errorf("password %q contains a look-alike character", out)
	}
}

func TestPwOptions(t *testing.T) {
	lines := strings.Split(mustRun(t, "", "pw", "32", "-n", "5", "--no-symbols"), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d passwords, want 5", len(lines))
	}
	for _, l := range lines {
		if len(l) != 32 || strings.ContainsAny(l, pwSymbols) {
			t.Errorf("bad password %q", l)
		}
	}
	if _, _, err := run(t, "", "pw", "2"); err == nil {
		t.Error("a 2-character password should be rejected")
	}
}

func TestLorem(t *testing.T) {
	if out := mustRun(t, "", "lorem"); !strings.HasPrefix(out, "Lorem ipsum dolor sit amet") {
		t.Errorf("lorem starts with %q", out[:min(30, len(out))])
	}
	if words := strings.Fields(mustRun(t, "", "lorem", "-w", "12")); len(words) != 12 {
		t.Errorf("-w 12 printed %d words", len(words))
	}
	if paras := strings.Split(mustRun(t, "", "lorem", "3"), "\n\n"); len(paras) != 3 {
		t.Errorf("lorem 3 printed %d paragraphs", len(paras))
	}
	if _, _, err := run(t, "", "lorem", "zero"); err == nil {
		t.Error("lorem zero should fail")
	}
}
