package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli"
	"github.com/jameymcelveen/zxcli/core"
)

// newApp builds an App on the real manifest with captured output and no
// user or project config.
func newApp(t *testing.T, stdin string) (*core.App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	app, err := core.New(zxcli.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	app.Stdin = strings.NewReader(stdin)
	app.Stdout = &out
	app.Stderr = &errOut
	app.StdinTTY = stdin == ""
	app.Color = false
	return app, &out, &errOut
}

// run executes "zx <args>" and returns stdout, stderr and the error.
func run(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	app, out, errOut := newApp(t, stdin)
	err := app.Dispatch(args)
	return out.String(), errOut.String(), err
}

// mustRun is run for commands that are expected to succeed. It returns
// stdout with surrounding whitespace trimmed.
func mustRun(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, _, err := run(t, stdin, args...)
	if err != nil {
		t.Fatalf("zx %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}
