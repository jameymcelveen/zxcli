package commands

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("ai", runAI) }

// An aiProvider is one way of reaching a language model. zx never stores
// keys: providers use tools that are already signed in, or environment
// variables.
type aiProvider struct {
	ID     string
	Label  string
	Detect func(cfg core.Config) (ok bool, detail string)
	Ask    func(cfg core.Config, prompt string) (string, error)
}

// aiProviders is the list zx looks through. Tests add a stand-in.
var aiProviders = []aiProvider{
	{
		ID: "claude", Label: "Claude Code CLI",
		Detect: func(core.Config) (bool, string) {
			if path, err := core.LookPath("claude"); err == nil {
				return true, path
			}
			return false, "the claude command is not installed"
		},
		Ask: func(cfg core.Config, prompt string) (string, error) {
			// No tools and a neutral folder: zx wants one written answer, not
			// an agent exploring or changing the project.
			args := []string{"-p", prompt, "--tools", "", "--no-session-persistence"}
			if m := cfg.Setting("ai.model", ""); m != "" {
				args = append(args, "--model", m)
			}
			return runAITool(os.TempDir(), "", "claude", args...)
		},
	},
	{
		ID: "ollama", Label: "Ollama (local models)",
		Detect: func(cfg core.Config) (bool, string) {
			models, err := ollamaModels()
			switch {
			case err != nil:
				return false, "not running at " + ollamaHost()
			case len(models) == 0:
				return false, "running, but no models are pulled (try: ollama pull llama3.2)"
			}
			return true, fmt.Sprintf("%d models, using %s", len(models), cfg.Setting("ai.model", models[0]))
		},
		Ask: func(cfg core.Config, prompt string) (string, error) {
			model := cfg.Setting("ai.model", "")
			if model == "" {
				models, err := ollamaModels()
				if err != nil || len(models) == 0 {
					return "", errors.New("Ollama has no models to use")
				}
				model = models[0]
			}
			var reply struct {
				Response string `json:"response"`
			}
			err := postJSON(ollamaHost()+"/api/generate", nil,
				map[string]any{"model": model, "prompt": prompt, "stream": false}, &reply)
			return reply.Response, err
		},
	},
	{
		ID: "anthropic", Label: "Anthropic API",
		Detect: func(core.Config) (bool, string) {
			if os.Getenv("ANTHROPIC_API_KEY") != "" {
				return true, "ANTHROPIC_API_KEY is set"
			}
			return false, "ANTHROPIC_API_KEY is not set"
		},
		Ask: func(cfg core.Config, prompt string) (string, error) {
			var reply struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			err := postJSON("https://api.anthropic.com/v1/messages",
				map[string]string{"x-api-key": os.Getenv("ANTHROPIC_API_KEY"), "anthropic-version": "2023-06-01"},
				map[string]any{
					"model":      cfg.Setting("ai.model", "claude-sonnet-5-5"),
					"max_tokens": 4096,
					"messages":   []map[string]string{{"role": "user", "content": prompt}},
				}, &reply)
			if err != nil {
				return "", err
			}
			var text strings.Builder
			for _, part := range reply.Content {
				text.WriteString(part.Text)
			}
			return text.String(), nil
		},
	},
	{
		ID: "custom", Label: "Your own command",
		Detect: func(cfg core.Config) (bool, string) {
			if line := cfg.Setting("ai.command", ""); line != "" {
				return true, line
			}
			return false, `set "ai.command" in your config to any program that reads a prompt on stdin`
		},
		Ask: func(cfg core.Config, prompt string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			cmd := core.ShellCommandContext(ctx, cfg.Setting("ai.command", ""), "")
			cmd.Stdin = strings.NewReader(prompt)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				return "", fmt.Errorf("%v: %s", err, firstLine(stderr.String()))
			}
			return string(out), nil
		},
	},
}

func runAITool(dir, stdin, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %v: %s", name, err, firstLine(stderr.String()+" "+string(out)))
	}
	return string(out), nil
}

func ollamaHost() string {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		return "http://127.0.0.1:11434"
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	return strings.TrimRight(host, "/")
}

func ollamaModels() ([]string, error) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	res, err := client.Get(ollamaHost() + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tags); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

func postJSON(url string, headers map[string]string, body, into any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", res.Status, firstLine(string(raw)))
	}
	return json.Unmarshal(raw, into)
}

// askError wraps a failure from an AI with what to do about it, for the
// failures people actually run into.
func askError(providerID string, err error) error {
	hint := aiHint(providerID, err.Error())
	if hint == "" {
		hint = "Run 'zx ai test' to check it, or 'zx ai' to choose another AI."
	}
	return fmt.Errorf("%s did not answer: %w\n%s", providerID, err, hint)
}

// aiHint turns a provider error into advice. It returns "" when it has none.
func aiHint(providerID, msg string) string {
	m := strings.ToLower(msg)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(m, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("deadline exceeded", "timed out", "timeout"):
		return "It took too long to answer. Try again, or use a smaller or faster model (zx ai --model <name>)."
	case providerID == "claude" && has("oauth", "failed to authenticate", "not logged in", "please run /login", "authentication"):
		return "Claude Code needs you to sign in again. Run 'claude' and sign in (type /login if it does not ask), then try again."
	case providerID == "claude" && has("executable file not found", "no such file"):
		return "Claude Code is not installed or is not on your PATH. Install it, or run 'zx ai' to choose another AI."
	case providerID == "ollama" && has("connection refused", "no such host", "cannot assign requested address"):
		return "Ollama is not running. Start the Ollama app, or run 'ollama serve', then try again."
	case providerID == "ollama" && has("model") && has("not found", "404"):
		return "Ollama does not have that model. Run 'ollama pull <model>', or 'zx ai' to pick another."
	case providerID == "anthropic" && has("401", "invalid x-api-key", "authentication_error"):
		return "The Anthropic API rejected the key. Check that ANTHROPIC_API_KEY is correct and still active."
	case providerID == "anthropic" && has("429", "rate_limit", "credit balance", "overloaded"):
		return "The Anthropic API is rate limiting you or the account is out of credit. Wait a moment, or check your plan."
	case providerID == "anthropic" && has("no such host", "dial tcp", "network is unreachable"):
		return "zx could not reach api.anthropic.com. Check your internet connection."
	case providerID == "custom":
		return "Check the \"ai.command\" setting: it must be a program that reads the prompt on stdin and prints the reply."
	}
	return ""
}

func findProvider(id string) *aiProvider {
	for i := range aiProviders {
		if aiProviders[i].ID == id {
			return &aiProviders[i]
		}
	}
	return nil
}

// prompter asks the user questions on stderr and reads answers from stdin.
// One prompter is shared for a whole command so buffered input is not lost.
type prompter struct {
	c *core.Context
	r *bufio.Reader
}

func newPrompter(c *core.Context) *prompter {
	return &prompter{c: c, r: bufio.NewReader(c.Stdin)}
}

// ask prints a question and returns the trimmed answer. ok is false when
// there is no more input, which callers treat as "no".
func (p *prompter) ask(question string) (answer string, ok bool) {
	fmt.Fprint(p.c.Stderr, question)
	line, err := p.r.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(p.c.Stderr)
		return "", false
	}
	return strings.TrimSpace(line), true
}

func isYes(answer string) bool {
	a := strings.ToLower(answer)
	return a == "y" || a == "yes"
}

func runAI(c *core.Context) error {
	fs := c.Flags()
	local := fs.Bool("local", false, "save the choice for this project only")
	model := fs.String("model", "", "the `model` to ask for")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	sub := ""
	if len(pos) > 0 {
		sub = pos[0]
	}
	switch sub {
	case "":
		printAIStatus(c)
		if c.App.Config.Setting("ai.provider", "") == "" && c.App.StdinTTY {
			c.Println()
			_, err := pickProvider(c, newPrompter(c), *local)
			return err
		}
		return nil
	case "pick":
		_, err := pickProvider(c, newPrompter(c), *local)
		return err
	case "use":
		if len(pos) != 2 {
			return core.Usagef("say which one: zx ai use claude")
		}
		if findProvider(pos[1]) == nil {
			return core.Usagef("unknown AI %q: run 'zx ai' to see the choices", pos[1])
		}
		return saveAIChoice(c, pos[1], *model, *local)
	case "off":
		return saveAIChoice(c, "", "", *local)
	case "test":
		provider, err := ensureProvider(c, newPrompter(c))
		if err != nil {
			return err
		}
		reply, err := provider.Ask(c.App.Config, "Reply with the single word: ready")
		if err != nil {
			return askError(provider.ID, err)
		}
		c.Printf("%s answered: %s\n", provider.ID, firstLine(reply))
		return nil
	}
	return core.Usagef("unknown subcommand %q: use pick, use, test or off", sub)
}

func printAIStatus(c *core.Context) {
	cfg := c.App.Config
	chosen := cfg.Setting("ai.provider", "")
	if chosen == "" {
		c.Println("No AI is chosen yet. Commands like zx new \"...\" will ask you to pick one.")
	} else {
		where := "this machine"
		if cfg.ProjectDir != "" && core.ReadSettings(core.ProjectSettingsPath(cfg.ProjectDir))["ai.provider"] != "" {
			where = "this project"
		}
		line := "zx uses " + c.App.Bold(chosen) + " (chosen for " + where + ")"
		if m := cfg.Setting("ai.model", ""); m != "" {
			line += ", model " + m
		}
		c.Println(line)
	}
	c.Printf("\n%s\n", c.App.Bold("Found on this machine"))
	for _, p := range aiProviders {
		ok, detail := p.Detect(cfg)
		mark := c.App.Dim("no ")
		if ok {
			mark = c.App.Cyan("yes")
		}
		c.Printf("  %s  %s  %s  %s\n", mark, padRight(p.ID, 9), padRight(p.Label, 21), c.App.Dim(detail))
	}
	c.Println("\nzx sends the AI your request, the names of the files in the current folder,")
	c.Println("and your command names. It never sends file contents, and it stores no keys.")
}

// saveAIChoice records the provider (and model) in a settings file zx owns.
func saveAIChoice(c *core.Context, id, model string, local bool) error {
	cfg := c.App.Config
	path, where := core.UserSettingsPath(), "this machine"
	if local {
		root := cfg.ProjectDir
		if root == "" {
			var err error
			if root, err = workDir(); err != nil {
				return err
			}
		}
		path, where = core.ProjectSettingsPath(root), "this project"
	}
	if err := core.SaveSetting(path, "ai.provider", id); err != nil {
		return err
	}
	if model != "" || id == "" {
		if err := core.SaveSetting(path, "ai.model", model); err != nil {
			return err
		}
	}
	cfg.Settings["ai.provider"] = id
	if id == "" {
		delete(cfg.Settings, "ai.provider")
		c.Printf("AI turned off for %s\n", where)
		return nil
	}
	if model != "" {
		cfg.Settings["ai.model"] = model
	}
	c.Printf("zx will use %s for %s  %s\n", id, where, c.App.Dim(path))
	return nil
}

// pickProvider shows what is installed and lets the user choose.
func pickProvider(c *core.Context, p *prompter, local bool) (*aiProvider, error) {
	var found []*aiProvider
	for i := range aiProviders {
		if ok, _ := aiProviders[i].Detect(c.App.Config); ok {
			found = append(found, &aiProviders[i])
		}
	}
	if len(found) == 0 {
		return nil, errors.New("no AI found on this machine. Install Claude Code or Ollama, or set ANTHROPIC_API_KEY, then run: zx ai")
	}
	fmt.Fprintln(c.Stderr, "Which AI should zx use?")
	for i, pr := range found {
		fmt.Fprintf(c.Stderr, "  %d) %s  %s\n", i+1, padRight(pr.ID, 9), pr.Label)
	}
	answer, ok := p.ask(fmt.Sprintf("Pick a number, 1 to %d (Enter to cancel): ", len(found)))
	n, err := strconv.Atoi(answer)
	if !ok || err != nil || n < 1 || n > len(found) {
		return nil, errors.New("no AI chosen. Pick one any time with: zx ai")
	}
	chosen := found[n-1]
	if !local && c.App.Config.ProjectDir != "" {
		scope, _ := p.ask("Use it [1] everywhere on this machine, or [2] only in this project? [1] ")
		local = scope == "2"
	}
	if err := saveAIChoice(c, chosen.ID, "", local); err != nil {
		return nil, err
	}
	return chosen, nil
}

// ensureProvider returns the chosen AI, asking the user to pick one first if
// they have not yet.
func ensureProvider(c *core.Context, p *prompter) (*aiProvider, error) {
	id := c.App.Config.Setting("ai.provider", "")
	if id == "" {
		return pickProvider(c, p, false)
	}
	provider := findProvider(id)
	if provider == nil {
		return nil, fmt.Errorf("the chosen AI %q is not one zx knows. Pick again with: zx ai pick", id)
	}
	if ok, detail := provider.Detect(c.App.Config); !ok {
		return nil, fmt.Errorf("%s is chosen but not available (%s). Pick again with: zx ai pick", id, detail)
	}
	return provider, nil
}

// workDir is the current folder. Tests replace it.
var workDir = os.Getwd
