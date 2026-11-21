package commands

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestSaversStayOnScreen(t *testing.T) {
	for _, style := range []string{"stars", "rain", "bounce"} {
		for _, size := range [][2]int{{1, 1}, {2, 1}, {20, 5}, {80, 24}, {200, 60}} {
			sv, err := newSaver(style, rand.New(rand.NewSource(1)))
			if err != nil || sv == nil {
				t.Fatalf("%s: %v", style, err)
			}
			g := newGrid(size[0], size[1])
			for i := 0; i < 400; i++ {
				g.clear()
				sv.step(g, time.Duration(i)*time.Second)
			}
			out := g.render(true)
			if got := strings.Count(out, "\r\n"); got != g.h-1 {
				t.Errorf("%s %v: %d line breaks, want %d", style, size, got, g.h-1)
			}
		}
	}
}

func TestSaverStylesAndNone(t *testing.T) {
	if sv, err := newSaver("none", nil); sv != nil || err != nil {
		t.Errorf("none should give no saver and no error, got %v %v", sv, err)
	}
	if _, err := newSaver("disco", nil); err == nil {
		t.Error("an unknown style should be an error")
	}
}

func TestQuitKey(t *testing.T) {
	keys := func(b ...byte) <-chan byte {
		ch := make(chan byte, len(b))
		for _, x := range b {
			ch <- x
		}
		return ch
	}
	if !quitKey('q', keys()) || !quitKey(3, keys()) {
		t.Error("q and Ctrl-C should quit")
	}
	if !quitKey(0x1b, keys()) {
		t.Error("a lone Esc should quit")
	}
	if quitKey(0x1b, keys('[', 'A')) {
		t.Error("an arrow key must not quit")
	}
	if quitKey('x', keys()) {
		t.Error("other keys must not quit")
	}
}

func TestElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{
		5 * time.Second: "5s", 65 * time.Second: "1m05s", 3725 * time.Second: "1h02m",
	} {
		if got := cafElapsed(d); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
}

func TestCaffeinateHoldsAndReleases(t *testing.T) {
	held, released := false, false
	keepAwakeFn = func(string, bool) (func(), string, error) {
		held = true
		return func() { released = true }, "test", nil
	}
	defer func() { keepAwakeFn = keepAwake }()
	rawModeFn = func() (func(), error) { return func() {}, nil }
	defer func() { rawModeFn = rawMode }()
	out, _, err := run(t, "x", "caffeinate", "--for", "50ms")
	if err != nil {
		t.Fatal(err)
	}
	if !held || !released || !strings.Contains(out, "awake for") {
		t.Errorf("held=%v released=%v out=%q", held, released, out)
	}
}
