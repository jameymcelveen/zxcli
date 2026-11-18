// Package zxcli holds the embedded command manifest for the zx CLI.
//
// zxcli.json is the registry of built-in commands. A command exists when two
// things are true: its Go file in ./commands calls core.Register, and it has
// an entry in zxcli.json. The manifest carries the name, summary, aliases,
// group and examples; the Go file carries the behavior.
package zxcli

import _ "embed"

// Manifest is the raw contents of zxcli.json, compiled into the binary.
//
//go:embed zxcli.json
var Manifest []byte
