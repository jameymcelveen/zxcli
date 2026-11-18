# zx

A pluggable command-line toolbox for everyday developer chores. One small binary, no runtime, no dependencies.

```console
$ zx guid
3f2b8c1e-9d47-4a6b-8e21-5c0f7a9d1b34

$ zx guid --copy          # any command can put its output on the clipboard
$ zx now 1700000000       # epoch -> local, UTC and ISO time
$ zx port 3000 -k         # see what is on port 3000 and stop it
$ curl -s api.example.com | zx json
```

Run `zx` on its own to see every command, and `zx help <command>` for details.

## Install

**macOS and Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/jameymcelveen/zxcli/main/scripts/install.sh | sh
```

**Windows** (PowerShell)

```powershell
irm https://raw.githubusercontent.com/jameymcelveen/zxcli/main/scripts/install.ps1 | iex
```

**Installers** are attached to each [release](https://github.com/jameymcelveen/zxcli/releases/latest):

| System | File | Installs to |
| --- | --- | --- |
| macOS (Intel and Apple silicon) | `zx_darwin_universal.pkg` | `/usr/local/bin/zx` |
| Windows | `zx_windows_amd64_setup.exe` | `%LOCALAPPDATA%\Programs\zx`, added to your PATH |
| Debian, Ubuntu | `zx_linux_amd64.deb` | `/usr/bin/zx` |
| Fedora, RHEL | `zx_linux_amd64.rpm` | `/usr/bin/zx` |

The installers are not code-signed. macOS asks you to confirm the `.pkg` (right-click it and choose Open), and Windows SmartScreen shows a "More info" prompt for the setup program. The install scripts above avoid both prompts and verify the download against the release checksums.

**From source** (needs Go 1.22 or newer): `make install`

## Commands

| Command | What it does |
| --- | --- |
| `zx guid` | Generate GUIDs / UUIDs: `zx guid 5`, `-u` uppercase, `-7` time-ordered v7, `--bare`, `-b` braces |
| `zx pw` | Strong random passwords with no look-alike characters: `zx pw 32`, `--no-symbols` |
| `zx lorem` | Placeholder text: `zx lorem 3` paragraphs, `-w 12` words |
| `zx now` | The time in local, UTC, ISO and epoch form, or convert one: `zx now 1700000000` |
| `zx b64` | Base64 encode, or decode with `-d` |
| `zx url` | URL-encode, decode with `-d`, or take a URL apart with `-p` |
| `zx case` | Convert naming styles: `zx case snake HelloWorld` |
| `zx slug` | `"Hello, World!"` becomes `hello-world` |
| `zx json` | Pretty-print, minify (`-m`) or validate JSON |
| `zx jwt` | Decode a JWT and show when it expires |
| `zx hash` | SHA-256 (or `-a md5`, `sha1`, `sha512`) of files, text or stdin |
| `zx port` | What is listening on a port, and `-k` to stop it |
| `zx ip` | Local IP addresses |
| `zx serve` | Serve a folder over HTTP: `zx serve ./dist -p 3000` |
| `zx config` | Show, create (`init`) or edit your config |
| `zx completion` | Shell completion for bash, zsh, fish and PowerShell |

Text commands take their input as arguments or from a pipe. `--copy` works on every command, including your own.

## Make it yours

There are four ways to add a command. Pick the lightest one that does the job.

### 1. Your own commands and aliases (no code)

Run `zx config init` to create `~/.config/zx/zxcli.json` (`%AppData%\zx\zxcli.json` on Windows). Comments and trailing commas are allowed.

```jsonc
{
  "aliases": { "g": "guid", "id": "guid --copy" },
  "commands": [
    { "name": "gs", "summary": "git status, short form", "run": "git status -sb" },
    { "name": "deploy", "summary": "ship it", "run": "./scripts/deploy.sh \"$1\"" }
  ],
  "disabled": ["lorem"]
}
```

`run` is a shell line. Extra arguments are appended to it, or placed where the line uses `$1`, `$2` or `"$@"`.

### 2. Project commands

Put a `.zx.json` in a project folder, using the same format. Its commands appear whenever you are in that folder or below it, and they run from the folder that holds the file. This repo has one: inside it, `zx check` runs lint and tests.

### 3. Plugins in any language

Any program on your PATH named `zx-<name>` becomes `zx <name>`, the way git plugins work. Write it in bash, Python, C#, anything.

### 4. Built-in commands in Go

Every built-in command is one Go file in [`commands/`](commands) plus one entry in [`zxcli.json`](zxcli.json), the command registry that is compiled into the binary. The scaffolder writes both, along with a test:

```console
$ zx new dice -s "Roll some dice" -g Generate
wrote commands/dice.go
wrote commands/dice_test.go
wrote zxcli.json
```

The Go file registers the behavior:

```go
func init() { core.Register("dice", runDice) }

func runDice(c *core.Context) error {
	fs := c.Flags()
	sides := fs.Int("sides", 6, "number of sides")
	args, err := c.Parse(fs) // flags may come before or after arguments
	if err != nil {
		return err
	}
	// ...
	c.Println(result)
	return nil
}
```

The manifest entry supplies everything help needs:

```json
{
  "name": "dice",
  "summary": "Roll some dice",
  "usage": "zx dice [count] [flags]",
  "group": "Generate",
  "aliases": ["roll"],
  "examples": ["zx dice 2 --sides 20"]
}
```

A test fails the build if the two ever disagree: a registered command with no manifest entry, an entry with no code, or a name used twice. Set `"disabled": true` on an entry to switch a built-in off without deleting it.

## Shell completion

```sh
source <(zx completion zsh)        # or bash; add it to your shell profile
zx completion fish > ~/.config/fish/completions/zx.fish
zx completion powershell | Out-String | Invoke-Expression
```

Completion includes your own commands, project commands and plugins.

## Develop

```console
$ make            # list the targets
$ make check      # gofmt, go vet (including for Windows), tests
$ make install    # build and copy to /usr/local/bin
$ make installers # every archive and installer this machine can build, into dist/
```

| Layout | |
| --- | --- |
| `cmd/zx/` | `main`, a few lines |
| `core/` | registry, dispatcher, config, help |
| `commands/` | one file per command, plus tests |
| `zxcli.json` | the built-in command registry |
| `packaging/`, `scripts/` | installers and install scripts |

To release, push a tag: `git tag v0.2.0 && git push origin v0.2.0`. GitHub Actions builds every installer, installs each one on a real Linux, macOS and Windows machine, runs `zx`, and only then publishes the release. The macOS `.pkg` is built, test-installed and attached by a follow-up job. If GitHub has no macOS machine free, run `make upload-pkg VERSION=v0.2.0` on a Mac.

## License

[MIT](LICENSE)
