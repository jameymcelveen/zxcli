// Command zx is a pluggable toolbox for everyday developer chores.
package main

import (
	"fmt"
	"os"

	"github.com/jameymcelveen/zxcli"
	"github.com/jameymcelveen/zxcli/commands" // also registers the built-in commands
	"github.com/jameymcelveen/zxcli/core"
)

func main() {
	app, err := core.New(zxcli.Manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zx:", err)
		os.Exit(1)
	}
	app.Wrap = commands.UpdateNotice(app)
	os.Exit(app.Run(os.Args[1:]))
}
