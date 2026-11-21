package commands

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("caff", runCaffeinate) }

// Hooks so tests do not touch the real power settings or the terminal.
var (
	keepAwakeFn  = keepAwake
	rawModeFn    = rawMode
	termSizeFn   = termSize
	displayOffFn = displayOff
	fullscreenFn = goFullscreen
)

func runCaffeinate(c *core.Context) error {
	fullscreenHint := ""
	flags := c.Flags()
	dur := flags.Duration("for", 0, "stop after this `duration`, such as 90m or 2h (default: until you press Esc)")
	style := flags.String("style", "rain", "screen saver: rain, stars, bounce or none")
	fullscreen := flags.Bool("fullscreen", true, "on macOS Terminal and iTerm2, fill the screen while it runs (--fullscreen=false to stay in the window)")
	screenOff := flags.Duration("screen-off", 10*time.Minute, "turn the screen off after this long without a key press; 0 keeps it on")
	pos, err := c.Parse(flags)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return core.Usagef("zx caff takes at most one duration, such as 2h")
	}
	if len(pos) == 1 && *dur == 0 {
		if *dur, err = time.ParseDuration(pos[0]); err != nil || *dur <= 0 {
			return core.Usagef("%q is not a duration: try 90m or 2h", pos[0])
		}
	}
	sv, err := newSaver(*style, rand.New(rand.NewSource(time.Now().UnixNano())))
	if err != nil {
		return err
	}

	if *screenOff < 0 {
		return core.Usagef("--screen-off cannot be negative")
	}
	// With nothing drawn there is no reason to hold the screen on.
	drawing := sv != nil && core.IsTerminal(os.Stdin) && core.IsTerminal(os.Stdout)
	release, how, err := keepAwakeFn("zx caff", drawing || *screenOff == 0)
	if err != nil {
		return err
	}
	defer func() { release() }()
	// Switching takes the new hold before dropping the old one, so the
	// machine is never unprotected in between.
	setDisplay := func(on bool) {
		r, _, err := keepAwakeFn("zx caff", on)
		if err != nil {
			return
		}
		old := release
		release = r
		old()
		if !on {
			displayOffFn()
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *dur > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *dur)
		defer cancel()
	}

	start := time.Now()
	restore, rawOK := func() {}, false
	if core.IsTerminal(os.Stdin) {
		if r, err := rawModeFn(); err == nil {
			restore, rawOK = r, true
		}
	}
	if !rawOK {
		c.Printf("keeping this machine awake (%s). Ctrl-C to stop.\n", how)
		<-ctx.Done()
	} else {
		keys := readKeys(os.Stdin)
		if sv == nil || !core.IsTerminal(os.Stdout) {
			c.Printf("keeping this machine awake (%s). Press Esc to stop.\n", how)
			waitForQuit(ctx, keys)
		} else {
			core.EnableVT(os.Stdout)
			leaveFull := func() {}
			if *fullscreen {
				var ferr error
				if leaveFull, ferr = fullscreenFn(); ferr != nil {
					leaveFull = func() {}
					fullscreenHint = "to go fullscreen, allow your terminal under System Settings > Privacy & Security > Accessibility, then run zx caff again"
				}
			}
			runSaver(ctx, c.Stdout, sv, keys, start, os.Getenv("NO_COLOR") == "", *screenOff, setDisplay)
			leaveFull()
		}
		restore()
	}
	c.Printf("awake for %s\n", cafElapsed(time.Since(start)))
	if fullscreenHint != "" {
		fmt.Fprintln(c.Stderr, "zx:", fullscreenHint)
	}
	return nil
}

// readKeys feeds raw bytes from the terminal into a channel.
func readKeys(r io.Reader) <-chan byte {
	ch := make(chan byte, 16)
	go func() {
		defer close(ch)
		buf := make([]byte, 1)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				ch <- buf[0]
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// quitKey reports whether a key means stop. A lone Esc does; Esc followed
// straight away by more bytes is an arrow key or similar, and is swallowed.
func quitKey(b byte, keys <-chan byte) bool {
	switch b {
	case 'q', 'Q', 3:
		return true
	case 0x1b:
		select {
		case <-keys:
			for {
				select {
				case <-keys:
				case <-time.After(20 * time.Millisecond):
					return false
				}
			}
		case <-time.After(40 * time.Millisecond):
			return true
		}
	}
	return false
}

func waitForQuit(ctx context.Context, keys <-chan byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-keys:
			if !ok {
				<-ctx.Done()
				return
			}
			if quitKey(b, keys) {
				return
			}
		}
	}
}

const (
	altScreenOn  = "\x1b[?1049h\x1b[?25l\x1b[2J"
	altScreenOff = "\x1b[0m\x1b[?25h\x1b[?1049l"
)

// runSaver draws on the alternate screen until Esc, q, Ctrl-C or the timeout.
// Everything on screen keeps moving, and the hint disappears after a few
// seconds, so nothing sits still long enough to burn in. After screenOff
// without a key press the screen is let go of and nothing is drawn; any key
// brings the saver back, and only Esc, q or Ctrl-C stops zx.
func runSaver(ctx context.Context, out io.Writer, sv saver, keys <-chan byte, start time.Time, color bool, screenOff time.Duration, setDisplay func(bool)) {
	io.WriteString(out, altScreenOn)
	defer io.WriteString(out, altScreenOff)
	w, h := termSizeFn()
	g := newGrid(w, h)
	checked, lastKey, off := time.Now(), time.Now(), false
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-keys:
			if !ok {
				keys = nil
			} else if quitKey(b, keys) {
				return
			} else {
				lastKey = time.Now()
				if off {
					off = false
					setDisplay(true)
					io.WriteString(out, "\x1b[2J")
				}
			}
		case <-tick.C:
			if off {
				continue
			}
			if screenOff > 0 && time.Since(lastKey) >= screenOff {
				off = true
				io.WriteString(out, "\x1b[2J")
				setDisplay(false)
				continue
			}
			if time.Since(checked) > time.Second {
				checked = time.Now()
				if nw, nh := termSizeFn(); nw != w || nh != h {
					w, h = nw, nh
					g = newGrid(w, h)
					io.WriteString(out, "\x1b[2J")
				}
			}
			g.clear()
			elapsed := time.Since(start)
			sv.step(g, elapsed)
			if time.Since(lastKey) < 6*time.Second {
				msg := " Esc to stop "
				g.text((g.w-len(msg))/2, g.h-1, msg, 250)
			}
			io.WriteString(out, g.render(color))
		}
	}
}

func cafElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// ---- the screen ----

type grid struct {
	w, h int
	ch   []rune
	col  []uint8
}

func newGrid(w, h int) *grid {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return &grid{w: w, h: h, ch: make([]rune, w*h), col: make([]uint8, w*h)}
}

func (g *grid) clear() {
	for i := range g.ch {
		g.ch[i], g.col[i] = 0, 0
	}
}

func (g *grid) set(x, y int, r rune, col uint8) {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return
	}
	g.ch[y*g.w+x], g.col[y*g.w+x] = r, col
}

func (g *grid) text(x, y int, s string, col uint8) {
	for _, r := range s {
		g.set(x, y, r, col)
		x++
	}
}

// render draws the grid from the top-left. It never writes a trailing
// newline, so the last row cannot scroll the screen.
func (g *grid) render(color bool) string {
	var b strings.Builder
	b.WriteString("\x1b[H")
	for y := 0; y < g.h; y++ {
		if y > 0 {
			b.WriteString("\r\n")
		}
		var last uint8
		for x := 0; x < g.w; x++ {
			r, c := g.ch[y*g.w+x], g.col[y*g.w+x]
			if r == 0 {
				r = ' '
			} else if color && c != 0 && c != last {
				fmt.Fprintf(&b, "\x1b[38;5;%dm", c)
				last = c
			}
			b.WriteRune(r)
		}
		if last != 0 {
			b.WriteString("\x1b[0m")
		}
	}
	return b.String()
}

// ---- the savers ----

type saver interface {
	step(g *grid, elapsed time.Duration)
}

func newSaver(style string, r *rand.Rand) (saver, error) {
	switch style {
	case "stars":
		return &starfield{r: r}, nil
	case "rain":
		return &rainfall{r: r}, nil
	case "bounce":
		return &bouncer{r: r, dx: 0.5, dy: 0.25, col: 244}, nil
	case "none", "off":
		return nil, nil
	}
	return nil, core.Usagef("unknown style %q: use stars, rain, bounce or none", style)
}

// starfield flies through space. Stars are dim and always on the move.
type starfield struct {
	r     *rand.Rand
	w, h  int
	stars [][3]float64 // x, y in -1..1, z in 0..1 (distance)
}

func (f *starfield) spawn(anyDepth bool) [3]float64 {
	z := 1.0
	if anyDepth {
		z = 0.05 + f.r.Float64()*0.95
	}
	return [3]float64{f.r.Float64()*2 - 1, f.r.Float64()*2 - 1, z}
}

func (f *starfield) step(g *grid, _ time.Duration) {
	if g.w != f.w || g.h != f.h {
		f.w, f.h = g.w, g.h
		f.stars = make([][3]float64, g.w*g.h/50+1)
		for i := range f.stars {
			f.stars[i] = f.spawn(true)
		}
	}
	cx, cy := float64(g.w)/2, float64(g.h)/2
	for i := range f.stars {
		s := &f.stars[i]
		s[2] -= 0.012
		x, y := int(cx+s[0]/s[2]*cx*0.25), int(cy+s[1]/s[2]*cy*0.25)
		if s[2] <= 0.05 || x < 0 || y < 0 || x >= g.w || y >= g.h {
			*s = f.spawn(false)
			continue
		}
		switch {
		case s[2] > 0.66:
			g.set(x, y, '.', 240)
		case s[2] > 0.33:
			g.set(x, y, '+', 244)
		default:
			g.set(x, y, '*', 248)
		}
	}
}

// rainfall drops dim green code down every other column.
type rainfall struct {
	r     *rand.Rand
	w, h  int
	drops []rainDrop
}

type rainDrop struct {
	y, speed float64
	length   int
}

func (f *rainfall) reset(d *rainDrop, initial bool) {
	d.speed = 0.25 + f.r.Float64()*0.6
	d.length = 5 + f.r.Intn(11)
	d.y = -float64(d.length) - f.r.Float64()*float64(f.h)
	if initial {
		d.y = f.r.Float64()*float64(f.h) - float64(d.length)
	}
}

func (f *rainfall) step(g *grid, _ time.Duration) {
	if g.w != f.w || g.h != f.h {
		f.w, f.h = g.w, g.h
		f.drops = make([]rainDrop, (g.w+1)/2)
		for i := range f.drops {
			f.reset(&f.drops[i], true)
		}
	}
	const glyphs = "01<>/\\|=+-:"
	for i := range f.drops {
		d := &f.drops[i]
		d.y += d.speed
		if int(d.y)-d.length > g.h {
			f.reset(d, false)
		}
		for k := 0; k < d.length; k++ {
			col := uint8(22)
			switch {
			case k == 0:
				col = 40
			case k < 3:
				col = 34
			case k < 7:
				col = 28
			}
			g.set(i*2, int(d.y)-k, rune(glyphs[f.r.Intn(len(glyphs))]), col)
		}
	}
}

// bouncer drifts a small clock around, changing shade at each wall.
type bouncer struct {
	r      *rand.Rand
	x, y   float64
	dx, dy float64
	col    uint8
	init   bool
}

func (b *bouncer) step(g *grid, elapsed time.Duration) {
	label := " zx · awake " + cafElapsed(elapsed) + " "
	n := len([]rune(label))
	if !b.init {
		b.init = true
		b.x, b.y = b.r.Float64()*float64(max(g.w-n, 1)), b.r.Float64()*float64(max(g.h-1, 1))
	}
	b.x += b.dx
	b.y += b.dy
	shades := []uint8{240, 244, 248, 67, 108, 137}
	if b.x < 0 || b.x > float64(g.w-n) {
		b.dx = -b.dx
		b.x = clampF(b.x, 0, float64(max(g.w-n, 0)))
		b.col = shades[b.r.Intn(len(shades))]
	}
	if b.y < 0 || b.y > float64(g.h-1) {
		b.dy = -b.dy
		b.y = clampF(b.y, 0, float64(max(g.h-1, 0)))
		b.col = shades[b.r.Intn(len(shades))]
	}
	g.text(int(b.x), int(b.y), label, b.col)
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
