package core

import "os"

// IsTerminal reports whether f is a terminal rather than a pipe or a file.
func IsTerminal(f *os.File) bool { return isTerminal(f) }

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// colorEnabled follows the NO_COLOR convention and only colors real terminals.
func colorEnabled(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(f) && enableVT(f)
}

func (a *App) paint(code, s string) string {
	if !a.Color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Bold, Dim and Cyan style text when color is on, and return it unchanged
// when it is off.
func (a *App) Bold(s string) string { return a.paint("1", s) }
func (a *App) Dim(s string) string  { return a.paint("2", s) }
func (a *App) Cyan(s string) string { return a.paint("36", s) }
