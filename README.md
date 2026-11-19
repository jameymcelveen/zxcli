# zx

**Clone a project. Run one command. Your machine is ready.**

```console
$ zx doctor --discover
Looked at ~/dev/shop
Found:
  laravel       composer.json requires laravel/framework
  react (web/)  package.json lists react
  docker        docker-compose.yml

Stack: laravel
ok       php       8.4.1    /usr/local/bin/php
ok       composer  2.8.3    /usr/local/bin/composer
missing  vendor    not found
missing  .env      not found

Stack: react
ok       node      22.11.0  /usr/local/bin/node
missing  web/node_modules  not found

Suggested by AI
missing  redis-server  not installed  (docker-compose.yml runs redis)

5 of 12 checks need attention
$ zx doctor --fix        # shows every command, asks once, installs, checks again
```

`zx doctor` knows 16 stacks: .NET, React, Next.js, Vue, Angular, Node, Laravel, PHP, Python, Go, Rust, Java, Ruby, Rails, Docker and make. Name a stack (`zx doctor laravel`) or let zx look at the folder (`zx doctor --discover`).

zx is also a pluggable command-line toolbox for everyday developer chores. One small binary, no runtime, no dependencies.

```console
$ zx guid
3f2b8c1e-9d47-4a6b-8e21-5c0f7a9d1b34

$ zx guid --copy          # any command can put its output on the clipboard
$ zx now 1700000000       # epoch -> local, UTC and ISO time
$ zx port 3000 -k         # see what is on port 3000 and stop it
$ curl -s api.example.com | zx json
$ zx new "bump the version, update the readme, tag the repo"   # the AI writes the command
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
| `zx repos` | Which git repos under `~/dev` have uncommitted, unpushed or unfetched work |
| `zx init` | Starter files for a project: `.editorconfig`, `.gitignore`, MIT license, Makefile, CI workflow, `.zx.json` |
| `zx doctor` | Is this machine ready for the project? Knows 16 stacks, `--discover` looks at a folder, `--fix` installs what is missing |
| `zx port` | What is listening on a port, and `-k` to stop it |
| `zx ip` | Local IP addresses |
| `zx serve` | Serve a folder over HTTP: `zx serve ./dist -p 3000` |
| `zx clip`, `zx paste` | The clipboard as a pipe, the same on every OS: `zx paste \| zx json \| zx clip` |
| `zx upgrade` | Update zx to the latest release, verified against its checksums |
| `zx global` | Make your aliases and commands runnable without the `zx` prefix |
| `zx ai` | Choose which AI writes commands for you |
| `zx config` | Show, create (`init`) or edit your config |
| `zx completion` | Shell completion for bash, zsh, fish and PowerShell |

Text commands take their input as arguments or from a pipe. `--copy` works on every command, including your own. `repos`, `doctor`, `port`, `now` and `ip` take `--json` for scripting.

`zx init` detects Go, .NET and Node projects and never overwrites a file that is already there. `zx repos` only reads: it runs `git status` in each repo, and `git fetch` only when you pass `--fetch`.

## Make it yours

There are five ways to add a command. Pick the lightest one that does the job.

### 0. Describe it, and let the AI write it

```console
$ zx new "bump the version, update the readme, tag the repo"
asking claude...
? Patch, minor or major by default?
> patch

zx bump  Bump the version, update the README and tag the repo  (sh, 31 lines)
------------------------------------------------------------
#!/bin/sh
...
------------------------------------------------------------
Save it as zx bump? [y/N] y
saved .zx/commands/bump.sh
read it over, then run it with: zx bump
```

For a one-off job, skip the name and just say it:

```console
$ zx "rename every .jpeg in this folder to .jpg"
...the script is shown...
Run it now? [y/N]
It worked. Save it as zx fix-jpegs for next time? [y/N/never]
```

- **You choose the AI.** `zx ai` shows what is installed (Claude Code, Ollama, an Anthropic API key, or your own command) and remembers your pick for the machine, or with `--local` for one project. The first AI command asks if you have not picked yet. `zx ai test` checks that it answers.
- **Questions get answers.** Ask how something works, `zx "how do I make an alias"`, and zx prints the answer instead of writing a script. Nothing is saved or run.
- **zx always shows the script first.** `zx new` saves and never runs. A sentence runs only after you answer yes, every time. `never` turns off the offer to save, not the question about running.
- **What is sent:** your request, the names of the files in the current folder, and your command names. File contents are not sent, and zx stores no keys.
- **It is still a script someone else wrote.** Read it before you run it, the same as you would a script from the internet.

### Script files

A generated command is an ordinary file, and you can write them by hand too. Any script in `.zx/commands/` (for a project) or `~/.config/zx/commands/` (for you) is a command named after the file:

| File | Runs with |
| --- | --- |
| `deploy.sh` | `sh` (bash from Git for Windows on Windows) |
| `report.js` | Node.js, Bun or Deno |
| `sync.ts` | Bun, Deno, tsx, or Node.js 23.6 or newer |
| `clean.py` | `python3` |
| `setup.ps1` | PowerShell |

Put `# summary: what it does` (or `// summary:`) near the top and it shows in `zx` help. Project scripts run from the project root. If the tool for a file type is not installed, zx says which one is needed.

### 1. Your own commands and aliases (no code)

Run `zx config init` to create `~/.config/zx/zxcli.json` (`%AppData%\zx\zxcli.json` on Windows). Comments and trailing commas are allowed.

```jsonc
{
  "aliases": { "g": "guid", "id": "guid --copy" },
  "commands": [
    { "name": "gs", "summary": "git status, short form", "run": "git status -sb" },
    { "name": "deploy", "summary": "ship it", "run": "./scripts/deploy.sh \"$1\"" }
  ],
  "disabled": ["lorem"],
  "settings": {
    "repos.dir": "~/dev",
    "doctor.tools": "git, gh, go, dotnet, node, docker"
  }
}
```

`run` is a shell line. Extra arguments are appended to it, or placed where the line uses `$1`, `$2` or `"$@"`.

#### Global commands

Mark an alias or command `"global": true` and it also works without the `zx` prefix, anywhere on the machine:

```jsonc
"aliases": {
  "bootstrap": { "run": "doctor --fix", "global": true }
}
```

```console
$ zx global sync
linked bootstrap
$ bootstrap          # same as: zx doctor --fix
```

`zx global` lists each one as `linked`, `not linked` or `taken`. A name is taken when a program with that name is already on your PATH, or when it is a shell built-in such as `cd`; zx leaves those alone and tells you to pick another name. It cannot see aliases and functions defined inside your shell profile, so check those yourself.

The launchers are small files in one folder (`zx global path`), which has to be on your PATH; `zx global` prints the line to add if it is not. zx only ever overwrites or deletes launchers it created. Only your own config can make a command global. A project's `.zx.json` cannot.

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

## zx doctor

`zx doctor` answers one question: can this machine do the work? It checks two lists and reports them separately.

- **This machine:** the `"doctor"` list in your own config. With no list, it checks a default set (git, gh, go, dotnet, node, npm, python3, docker, make).
- **This project:** the `"doctor"` list in the nearest `.zx.json`. A project's entry for a tool replaces your machine's entry for the same tool.

```jsonc
// .zx.json
{
  "doctor": [
    "git",
    { "tool": "dotnet", "min": "10.0" },
    { "tool": "terraform",
      "install": { "mac": "brew install terraform", "windows": "winget install -e --id Hashicorp.Terraform" } },
    { "name": "database is up", "run": "pg_isready -q", "install": "docker compose up -d db" },
    { "name": "API key is set", "run": "test -n \"$API_KEY\"" }
  ]
}
```

```console
$ zx doctor
This machine  ~/.config/zx/zxcli.json
ok       git           2.50.1    /usr/bin/git
ok       gh signed in

This project  ~/dev/api/.zx.json
too old  dotnet        9.0.100 (needs 10.0+)
missing  terraform     not installed
failed   database is up

3 of 5 checks need attention

Fixes  run them all with: zx doctor --fix
  brew upgrade --cask dotnet-sdk
  brew install terraform
  docker compose up -d db
```

A check is either a **tool** (with an optional minimum version) or a **named command** that must exit 0. `install` and `upgrade` are one command for every OS, or one each for `mac`, `windows` and `linux`. For common tools zx already knows the command for Homebrew, winget and apt, so most entries need no `install` line.

| | |
| --- | --- |
| `zx doctor` | Report only. It installs nothing. |
| `zx doctor laravel react` | Add stacks to the check (also `--stack`). `zx doctor --list` shows them all. A stack reads your project's files for the versions it needs. `name@folder` points at a subfolder, such as `react@web`. |
| `zx doctor --discover` | Look at this folder and list every stack it finds, then check them. Optionally asks an AI what else the project needs; see below. |
| `zx doctor --fix` | Show every install command, ask once, run them, then check again. `--yes` skips the question. |
| `zx doctor --trust` | Approve a project's custom `run` checks. |
| `zx doctor --strict` | Exit 1 if anything needs attention, for CI or a setup script. |
| `zx doctor --json` | The report as JSON. |

### Stacks and discovery

A stack is a ready-made set of checks. `laravel` checks PHP at the version `composer.json` asks for, Composer, that `vendor/` is installed and that `.env` exists, and Node too when there is a `package.json`. `dotnet` reads the SDK version from `global.json` or the project files, `go` from `go.mod`, and so on. Fixes for a subfolder run in that subfolder.

`zx doctor --discover` finds the stacks in the current folder and its immediate subfolders without any AI, then shows what it found and checks it. When you are at a terminal and an AI is set up (`zx ai`), it can also ask that AI what else the project needs, such as redis or a database client. Before anything is sent, zx lists exactly which files it would send and asks. `.env` is never read, `.env.example` is sent as variable names only, secret-looking lines are redacted, and the total is capped at about 20 KB. `--no-ai` skips the question.

Afterwards zx offers to save the result so a plain `zx doctor` checks it every time. It writes only its own files, `.zx/settings.json` (`doctor.stacks`) and `.zx/doctor.json`, and never touches your `.zx.json`. Commit them to share the setup with your team.

A project's `.zx.json` is written by whoever wrote the repo, so zx is careful with it. Its custom `run` checks are shown as `skipped` until you approve them with `--trust`, and they need approving again whenever they change. Install commands never run without `--fix`, and `--fix` always prints them first.

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
