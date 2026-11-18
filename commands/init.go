package commands

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("init", runInit) }

// InitOptions controls what "zx init" writes.
type InitOptions struct {
	Kind      string // go, dotnet, node or plain; empty means detect
	Owner     string // name for the license; empty means git user.name
	NoCI      bool
	NoLicense bool
	NoGit     bool
	DryRun    bool
}

// InitResult is one file "zx init" looked at.
type InitResult struct {
	File   string
	Action string // wrote, kept or would write
}

func runInit(c *core.Context) error {
	fs := c.Flags()
	var o InitOptions
	fs.StringVar(&o.Kind, "kind", "", "project `type`: go, dotnet, node or plain (default: detect)")
	fs.StringVar(&o.Owner, "owner", "", "`name` for the license (default: your git user.name)")
	fs.BoolVar(&o.NoCI, "no-ci", false, "skip the GitHub Actions workflow")
	fs.BoolVar(&o.NoLicense, "no-license", false, "skip the MIT license")
	fs.BoolVar(&o.NoGit, "no-git", false, "do not run git init")
	fs.BoolVar(&o.DryRun, "dry-run", false, "show what would be written, and write nothing")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) > 0 {
		dir = pos[0]
	}
	kind, results, err := InitProject(dir, o)
	if err != nil {
		return err
	}
	c.Printf("%s project\n", kind)
	for _, r := range results {
		c.Printf("%-11s %s\n", r.Action, r.File)
	}
	return nil
}

// DetectKind guesses the project type from the files in dir.
func DetectKind(dir string) string {
	has := func(patterns ...string) bool {
		for _, p := range patterns {
			if m, _ := filepath.Glob(filepath.Join(dir, p)); len(m) > 0 {
				return true
			}
		}
		return false
	}
	switch {
	case has("go.mod"):
		return "go"
	case has("*.sln", "*.slnx", "*.csproj", "*.fsproj"):
		return "dotnet"
	case has("package.json"):
		return "node"
	}
	return "plain"
}

// InitProject drops the standard starter files into dir. It never overwrites
// a file that is already there.
func InitProject(dir string, o InitOptions) (kind string, results []InitResult, err error) {
	kind = o.Kind
	if kind == "" {
		kind = DetectKind(dir)
	}
	tpl, ok := initKinds[kind]
	if !ok {
		return "", nil, core.Usagef("unknown project type %q: use go, dotnet, node or plain", kind)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, err
	}
	owner := o.Owner
	if owner == "" {
		owner = toolOutput("git", "config", "user.name")
	}
	if owner == "" {
		if u, err := user.Current(); err == nil {
			owner = u.Username
		}
	}
	fill := strings.NewReplacer("@NAME@", filepath.Base(abs), "@OWNER@", owner, "@YEAR@", strconv.Itoa(time.Now().Year()))

	files := [][2]string{
		{".editorconfig", initEditorconfig},
		{".gitignore", tpl.gitignore + initGitignoreCommon},
		{"README.md", "# @NAME@\n"},
		{".zx.json", tpl.zx},
	}
	if !o.NoLicense {
		files = append(files, [2]string{"LICENSE", initMIT})
	}
	if tpl.makefile != "" {
		files = append(files, [2]string{"Makefile", initMakefileHead + tpl.makefile})
	}
	if tpl.ci != "" && !o.NoCI {
		files = append(files, [2]string{filepath.Join(".github", "workflows", "ci.yml"), initCIHead + tpl.ci})
	}

	if !o.DryRun {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", nil, err
		}
	}
	for _, f := range files {
		path := filepath.Join(abs, f[0])
		switch {
		case fileExists(path):
			results = append(results, InitResult{f[0], "kept"})
		case o.DryRun:
			results = append(results, InitResult{f[0], "would write"})
		default:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", nil, err
			}
			if err := os.WriteFile(path, []byte(fill.Replace(f[1])), 0o644); err != nil {
				return "", nil, err
			}
			results = append(results, InitResult{f[0], "wrote"})
		}
	}
	if !o.NoGit && !fileExists(filepath.Join(abs, ".git")) {
		if _, lookErr := exec.LookPath("git"); lookErr == nil {
			if o.DryRun {
				results = append(results, InitResult{"git repository", "would start"})
			} else if out, err := exec.Command("git", "-C", abs, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
				return "", nil, fmt.Errorf("git init: %v: %s", err, strings.TrimSpace(string(out)))
			} else {
				results = append(results, InitResult{"git repository", "started"})
			}
		}
	}
	return kind, results, nil
}

type initKind struct{ gitignore, makefile, ci, zx string }

const initGitignoreCommon = `
# everywhere
.DS_Store
Thumbs.db
.env
.env.local
*.log
`

const initEditorconfig = `root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
trim_trailing_whitespace = true
indent_style = space
indent_size = 2

[*.{cs,fs,py,java,kt,rs}]
indent_size = 4

[*.go]
indent_style = tab
indent_size = 4

[{Makefile,*.mk}]
indent_style = tab

[*.md]
trim_trailing_whitespace = false
`

const initMakefileHead = `.DEFAULT_GOAL := help
.PHONY: help build test lint check

help: ## Show this list
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

`

const initCIHead = `name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
`

func initZX(check, doctor string) string {
	return `// Project commands and requirements. Run "zx" in this folder to see the
// commands, and "zx doctor" to check that this machine can build it.
{
  "commands": [
    { "name": "check", "summary": "lint and test before pushing", "run": "` + check + `" },
  ],
  "doctor": [` + doctor + `],
}
`
}

var initKinds = map[string]initKind{
	"go": {
		gitignore: "/bin/\n/dist/\n*.test\ncoverage.out\n",
		makefile: `build: ## Build everything
	go build ./...

test: ## Run the tests
	go test ./...

lint: ## Check formatting and vet
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi
	go vet ./...

check: lint test ## Lint and test: run this before pushing
`,
		ci: `      - uses: actions/setup-go@v7
        with:
          go-version: stable
          cache: false
      - run: make check
`,
		zx: initZX("make check", `"git", "go", "make"`),
	},
	"dotnet": {
		gitignore: "bin/\nobj/\n.vs/\n*.user\nTestResults/\n",
		makefile: `build: ## Build the solution
	dotnet build

test: ## Run the tests
	dotnet test

lint: ## Check formatting
	dotnet format --verify-no-changes

check: lint test ## Lint and test: run this before pushing
`,
		ci: `      - uses: actions/setup-dotnet@v6
        with:
          dotnet-version: 10.0.x
      - run: make check
`,
		zx: initZX("make check", `"git", { "tool": "dotnet", "min": "10.0" }, "make"`),
	},
	"node": {
		gitignore: "node_modules/\ndist/\nbuild/\ncoverage/\n.next/\n",
		makefile: `build: ## Build
	npm run build --if-present

test: ## Run the tests
	npm test

lint: ## Lint
	npm run lint --if-present

check: lint test ## Lint and test: run this before pushing
`,
		ci: `      - uses: actions/setup-node@v7
        with:
          node-version: lts/*
      - run: npm ci
      - run: make check
`,
		zx: initZX("make check", `"git", "node", "npm", "make"`),
	},
	"plain": {
		gitignore: "",
		zx:        initZX("echo nothing to check yet", `"git"`),
	},
}

const initMIT = `MIT License

Copyright (c) @YEAR@ @OWNER@

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`
