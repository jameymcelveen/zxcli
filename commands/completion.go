package commands

import (
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

func init() {
	core.Register("completion", runCompletion)
	core.Register("__complete", runComplete)
}

var completionScripts = map[string]string{
	"bash": `# zx bash completion. Load it with:  source <(zx completion bash)
_zx() {
  local cur=${COMP_WORDS[COMP_CWORD]}
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=($(compgen -W "$(zx __complete 2>/dev/null)" -- "$cur"))
  else
    COMPREPLY=($(compgen -f -- "$cur"))
  fi
}
complete -o default -F _zx zx
`,
	"zsh": `#compdef zx
# zx zsh completion. Load it with:  source <(zx completion zsh)
_zx() {
  if (( CURRENT == 2 )); then
    local -a cmds
    cmds=(${(f)"$(zx __complete --describe 2>/dev/null)"})
    _describe 'command' cmds
  else
    _files
  fi
}
compdef _zx zx
`,
	"fish": `# zx fish completion. Save it with:  zx completion fish > ~/.config/fish/completions/zx.fish
complete -c zx -f -n '__fish_use_subcommand' -a '(zx __complete --tab 2>/dev/null)'
`,
	"powershell": `# zx PowerShell completion. Load it with:  zx completion powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName zx -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  if ($commandAst.CommandElements.Count -le 2) {
    zx __complete | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
      [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
  }
}
`,
}

func runCompletion(c *core.Context) error {
	pos, err := c.Parse(c.Flags())
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return core.Usagef("say which shell: bash, zsh, fish or powershell")
	}
	script, ok := completionScripts[strings.ToLower(pos[0])]
	if !ok {
		return core.Usagef("no completion for %q: use bash, zsh, fish or powershell", pos[0])
	}
	c.Printf("%s", script)
	return nil
}

// runComplete prints command names, one per line, for the completion scripts.
func runComplete(c *core.Context) error {
	fs := c.Flags()
	describe := fs.Bool("describe", false, "name:summary lines (zsh)")
	tab := fs.Bool("tab", false, "name<TAB>summary lines (fish)")
	if _, err := c.Parse(fs); err != nil {
		return err
	}
	for _, it := range c.App.Names() {
		switch {
		case *describe:
			c.Printf("%s:%s\n", strings.ReplaceAll(it.Name, ":", `\:`), it.Summary)
		case *tab:
			c.Printf("%s\t%s\n", it.Name, it.Summary)
		default:
			c.Println(it.Name)
		}
	}
	return nil
}
