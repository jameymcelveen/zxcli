package commands

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli/core"
)

// fakeAI stands in for a real model. It hands back scripted replies in order
// and records every prompt it was sent.
type fakeAI struct {
	replies []string
	prompts []string
}

func commandReply(name, summary, language, script string) string {
	b, _ := json.Marshal(map[string]string{"name": name, "summary": summary, "language": language, "script": script})
	return string(b)
}

// aiFixture gives a test its own config folder and project folder, installs
// the fake AI as the only provider, and returns a function that runs zx with
// the given stdin.
func aiFixture(t *testing.T, chosen bool, replies ...string) (ai *fakeAI, home, project string, run func(stdin string, args ...string) (string, string, error)) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("these tests run generated JavaScript, which needs Node.js")
	}
	ai = &fakeAI{replies: replies}
	home, project = t.TempDir(), t.TempDir()
	t.Setenv("ZX_CONFIG", filepath.Join(home, "zxcli.json"))
	if err := os.WriteFile(filepath.Join(project, core.ProjectFile), []byte(`{ "commands": [ { "name": "existing", "run": "echo hi" } ] }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if chosen {
		if err := core.SaveSetting(core.UserSettingsPath(), "ai.provider", "fake"); err != nil {
			t.Fatal(err)
		}
	}
	oldProviders, oldWorkDir := aiProviders, workDir
	aiProviders = []aiProvider{{
		ID: "fake", Label: "Stand-in for tests",
		Detect: func(core.Config) (bool, string) { return true, "always here" },
		Ask: func(_ core.Config, prompt string) (string, error) {
			ai.prompts = append(ai.prompts, prompt)
			if len(ai.replies) == 0 {
				return "", errors.New("the fake AI has nothing left to say")
			}
			reply := ai.replies[0]
			ai.replies = ai.replies[1:]
			return reply, nil
		},
	}}
	workDir = func() (string, error) { return project, nil }
	t.Cleanup(func() { aiProviders, workDir = oldProviders, oldWorkDir })

	run = func(stdin string, args ...string) (string, string, error) {
		cfg, warnings := core.LoadConfig(project)
		if len(warnings) > 0 {
			t.Fatalf("config warnings: %v", warnings)
		}
		a, out, errOut := newApp(t, stdin)
		if err := a.SetConfig(cfg); err != nil {
			t.Fatal(err)
		}
		err := a.Dispatch(args)
		return out.String(), errOut.String(), err
	}
	return ai, home, project, run
}

// A script every test machine can run: Node writes a marker file.
const markerScript = `require("fs").writeFileSync("marker.txt", "made by the script " + process.argv.slice(2).join(","));
console.log("wrote marker.txt");`

func TestExtractJSON(t *testing.T) {
	plain := commandReply("bump", "Bump it", "sh", "echo {hi}\n")
	for _, reply := range []string{
		plain,
		"```json\n" + plain + "\n```",
		"Sure! Here is the command:\n" + plain + "\nLet me know if you want changes.",
	} {
		g, err := extractJSON(reply)
		if err != nil || g.Name != "bump" || g.Script != "echo {hi}\n" {
			t.Errorf("extractJSON(%q) = %+v, %v", reply, g, err)
		}
	}
	if g, err := extractJSON(`{"questions": ["Patch or minor?"]}`); err != nil || len(g.Questions) != 1 {
		t.Errorf("questions reply: %+v %v", g, err)
	}
	if _, err := extractJSON("I cannot help with that."); err == nil {
		t.Error("a reply with no JSON should be an error")
	}
}

func TestNewWithADescriptionAsksThenSaves(t *testing.T) {
	ai, _, project, run := aiFixture(t, true,
		`{"questions": ["Patch, minor or major by default?"]}`,
		commandReply("bump", "Bump the version and tag the repo", "js", markerScript),
	)
	out, errOut, err := run("patch\ny\n", "new", "bump the version, update the readme, tag the repo")
	if err != nil {
		t.Fatal(err)
	}
	// The question reached the user, and the answer reached the AI.
	if !strings.Contains(errOut, "Patch, minor or major by default?") {
		t.Errorf("the AI's question was not shown:\n%s", errOut)
	}
	if len(ai.prompts) != 2 || !strings.Contains(ai.prompts[1], "Q: Patch, minor or major by default?\nA: patch") {
		t.Fatalf("the answer was not sent back; prompts:\n%s", strings.Join(ai.prompts, "\n=====\n"))
	}
	// The prompt describes the request and the folder, and warns off taken names.
	first := ai.prompts[0]
	for _, want := range []string{"bump the version, update the readme, tag the repo", ".zx.json", "existing", "guid", "must not delete files"} {
		if !strings.Contains(first, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	// The script was shown before the save question.
	if !strings.Contains(errOut, `writeFileSync("marker.txt"`) || strings.Index(errOut, "writeFileSync") > strings.Index(errOut, "Save it as") {
		t.Errorf("the script must be shown before asking to save:\n%s", errOut)
	}

	path := filepath.Join(project, ".zx", "commands", "bump.js")
	if !strings.Contains(out, "saved "+path) || !strings.Contains(out, "zx bump") {
		t.Errorf("output: %q", out)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"// summary: Bump the version and tag the repo", "// made by zx ai (fake)", `from: "bump the version, update the readme, tag the repo"`, "wrote marker.txt"} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("saved script is missing %q:\n%s", want, saved)
		}
	}
	if fileExists(filepath.Join(project, "marker.txt")) {
		t.Fatal("zx new must not run the script it wrote")
	}

	// It is now a real command: listed with its summary, and runnable with arguments.
	help, _, _ := run("")
	if !strings.Contains(help, "bump") || !strings.Contains(help, "Bump the version and tag the repo") {
		t.Errorf("help does not list the new command:\n%s", help)
	}
	if out, _, err := run("", "bump", "a", "b"); err != nil || !strings.Contains(out, "wrote marker.txt") {
		t.Fatalf("zx bump: %v %q", err, out)
	}
	if got, _ := os.ReadFile(filepath.Join(project, "marker.txt")); string(got) != "made by the script a,b" {
		t.Errorf("the command should run in the project folder with its arguments; marker = %q", got)
	}
}

func TestNewWithADescriptionCanBeDeclined(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", ""} {
		_, home, project, run := aiFixture(t, true, commandReply("tidy", "Tidy up", "js", markerScript))
		_, errOut, err := run(answer, "new", "tidy the folder")
		if err != nil || !strings.Contains(errOut, "nothing was saved") {
			t.Errorf("answer %q: %v\n%s", answer, err, errOut)
		}
		if fileExists(filepath.Join(project, ".zx", "commands")) || fileExists(filepath.Join(home, "commands")) {
			t.Errorf("answer %q still wrote a command", answer)
		}
	}
}

func TestNewSaveLocationsAndNameClashes(t *testing.T) {
	// --user saves beside the user config, not in the project.
	_, home, project, run := aiFixture(t, true, commandReply("logs", "Open the logs", "js", markerScript))
	if _, _, err := run("", "new", "open the staging logs", "--user", "--yes"); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(home, "commands", "logs.js")) || fileExists(filepath.Join(project, ".zx", "commands")) {
		t.Error("--user should save in the user commands folder only")
	}

	// A name that is taken (a built-in, then a project command) has to be changed.
	_, _, project, run = aiFixture(t, true, commandReply("guid", "Make ids", "js", markerScript))
	_, errOut, err := run("y\nexisting\nmake-ids\n", "new", "make some ids")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(errOut, "is already a command") != 2 || !fileExists(filepath.Join(project, ".zx", "commands", "make-ids.js")) {
		t.Errorf("name clash handling:\n%s", errOut)
	}
	if fileExists(filepath.Join(project, ".zx", "commands", "guid.js")) {
		t.Error("a generated command must not shadow a built-in")
	}
}

func TestNewStillScaffoldsGoForAPlainName(t *testing.T) {
	ai, _, _, run := aiFixture(t, true)
	_, _, err := run("", "new", "dice")
	if err == nil || !strings.Contains(err.Error(), "zxcli source folder") {
		t.Errorf("a single word is a Go scaffold request, got %v", err)
	}
	if len(ai.prompts) != 0 {
		t.Error("the AI should not be asked for a plain name")
	}
}

func TestSentenceRunsOnlyAfterYes(t *testing.T) {
	// "y" to run, then "never" to the save offer.
	_, _, project, run := aiFixture(t, true,
		commandReply("mark", "Write a marker file", "js", markerScript),
		commandReply("mark", "Write a marker file", "js", markerScript),
	)
	out, errOut, err := run("y\nnever\n", "write a marker file here")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(project, "marker.txt")
	if !fileExists(marker) || !strings.Contains(out, "wrote marker.txt") {
		t.Fatalf("the script should have run in the current folder:\n%s\n%s", out, errOut)
	}
	if strings.Index(errOut, "writeFileSync") > strings.Index(errOut, "Run it now?") {
		t.Errorf("the script must be shown before asking to run it:\n%s", errOut)
	}
	if !strings.Contains(errOut, "[y/N/never]") || !strings.Contains(errOut, "will not offer again") {
		t.Errorf("the save offer:\n%s", errOut)
	}
	if got := core.ReadSettings(core.UserSettingsPath())["ai.offer-save"]; got != "never" {
		t.Errorf("ai.offer-save = %q", got)
	}
	if fileExists(filepath.Join(project, ".zx", "commands")) {
		t.Error("nothing should have been saved")
	}

	// Next time the save offer is gone, but the run question never goes away.
	os.Remove(marker)
	_, errOut, err = run("y\n", "write a marker file here")
	if err != nil || !fileExists(marker) {
		t.Fatalf("second run: %v", err)
	}
	if strings.Contains(errOut, "for next time") || !strings.Contains(errOut, "Run it now?") {
		t.Errorf("after 'never':\n%s", errOut)
	}
}

func TestSentenceDeclinedAndSaved(t *testing.T) {
	// No to running, yes to saving: the script is kept but never executed.
	_, _, project, run := aiFixture(t, true, commandReply("mark", "Write a marker file", "js", markerScript))
	out, errOut, err := run("n\ny\n", "write a marker file")
	if err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(project, "marker.txt")) || !strings.Contains(errOut, "not run") {
		t.Errorf("declined scripts must not run:\n%s", errOut)
	}
	if !fileExists(filepath.Join(project, ".zx", "commands", "mark.js")) || !strings.Contains(out, "zx mark") {
		t.Errorf("it should have been saved:\n%s", out)
	}

	// No input at all means no to everything.
	_, _, project, run = aiFixture(t, true, commandReply("mark", "Write a marker file", "js", markerScript))
	if _, _, err := run("", "write a marker file here"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(project, "marker.txt")) || fileExists(filepath.Join(project, ".zx", "commands")) {
		t.Error("with no answers, nothing may run or be saved")
	}
}

func TestBadRepliesAreReported(t *testing.T) {
	ai, _, _, run := aiFixture(t, true, "I would rather chat.", "Still no JSON, sorry.")
	_, _, err := run("y\n", "new", "do a thing please")
	if err == nil || !strings.Contains(err.Error(), "did not answer in the expected form") {
		t.Errorf("got %v", err)
	}
	if len(ai.prompts) != 2 || !strings.Contains(ai.prompts[1], "could not be used") {
		t.Error("zx should retry once, telling the AI what was wrong")
	}

	// An unknown language, an unusable name and an empty script are refused.
	for _, reply := range []string{
		commandReply("x", "s", "cobol", "DISPLAY 'HI'."),
		commandReply("Not A Name!", "s", "js", "1"),
		commandReply("x", "s", "js", "  "),
	} {
		_, _, _, run := aiFixture(t, true, reply, reply)
		if _, _, err := run("y\n", "new", "do a thing please"); err == nil {
			t.Errorf("reply %s should have been refused", reply)
		}
	}
}

func TestPickingAnAI(t *testing.T) {
	// With nothing chosen, an AI command asks which AI to use, then carries on.
	ai, home, _, run := aiFixture(t, false, commandReply("mark", "Write a marker file", "js", markerScript))
	_, errOut, err := run("1\n1\nn\n", "new", "write a marker file")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Which AI should zx use?") || !strings.Contains(errOut, "1) fake") || len(ai.prompts) != 1 {
		t.Errorf("the picker should run first:\n%s", errOut)
	}
	if got := core.ReadSettings(filepath.Join(home, "settings.json"))["ai.provider"]; got != "fake" {
		t.Errorf("the choice was not saved for the machine: %q", got)
	}

	// Choosing "only in this project" writes the project's settings file.
	_, home, project, run := aiFixture(t, false)
	if _, _, err := run("1\n2\n", "ai", "pick"); err != nil {
		t.Fatal(err)
	}
	if core.ReadSettings(core.ProjectSettingsPath(project))["ai.provider"] != "fake" || fileExists(filepath.Join(home, "settings.json")) {
		t.Error("a project-only choice belongs in .zx/settings.json")
	}
	if out, _, _ := run("", "ai"); !strings.Contains(out, "chosen for this project") || !strings.Contains(out, "yes  fake") {
		t.Errorf("status:\n%s", out)
	}

	// Cancelling the picker stops the command with a clear message.
	_, _, _, run = aiFixture(t, false)
	if _, _, err := run("\n", "write a marker file here"); err == nil || !strings.Contains(err.Error(), "zx ai") {
		t.Errorf("cancelled pick: %v", err)
	}
}

func TestAISubcommands(t *testing.T) {
	_, home, project, run := aiFixture(t, false, "ready")
	if out, _, err := run("", "ai", "use", "fake", "--model", "tiny"); err != nil || !strings.Contains(out, "zx will use fake for this machine") {
		t.Fatalf("ai use: %v %q", err, out)
	}
	settings := core.ReadSettings(filepath.Join(home, "settings.json"))
	if settings["ai.provider"] != "fake" || settings["ai.model"] != "tiny" {
		t.Errorf("settings = %v", settings)
	}
	if out, _, err := run("", "ai", "test"); err != nil || !strings.Contains(out, "fake answered: ready") {
		t.Errorf("ai test: %v %q", err, out)
	}
	if out, _, _ := run("", "ai"); !strings.Contains(out, "zx uses fake") || !strings.Contains(out, "model tiny") || !strings.Contains(out, "never sends file contents") {
		t.Errorf("status:\n%s", out)
	}
	if _, _, err := run("", "ai", "use", "hal9000"); err == nil {
		t.Error("an unknown AI should be refused")
	}
	if _, _, err := run("", "ai", "use", "fake", "--local"); err != nil || core.ReadSettings(core.ProjectSettingsPath(project))["ai.provider"] != "fake" {
		t.Errorf("--local: %v", err)
	}
	if out, _, err := run("", "ai", "off"); err != nil || !strings.Contains(out, "turned off") || core.ReadSettings(filepath.Join(home, "settings.json"))["ai.provider"] != "" {
		t.Errorf("ai off: %v %q", err, out)
	}

	// A chosen AI that has gone missing is reported, not silently swapped.
	if err := core.SaveSetting(core.ProjectSettingsPath(project), "ai.provider", ""); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveSetting(filepath.Join(home, "settings.json"), "ai.provider", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run("", "ai", "test"); err == nil || !strings.Contains(err.Error(), "zx ai pick") {
		t.Errorf("a missing provider: %v", err)
	}
}

func TestNoAIInstalled(t *testing.T) {
	_, _, _, run := aiFixture(t, false)
	aiProviders = []aiProvider{{ID: "none", Label: "Nothing", Detect: func(core.Config) (bool, string) { return false, "not installed" }}}
	_, _, err := run("1\n", "new", "do a thing please")
	if err == nil || !strings.Contains(err.Error(), "no AI found on this machine") {
		t.Errorf("got %v", err)
	}
}

func TestHowToQuestionsGetAnAnswerNotAScript(t *testing.T) {
	answer := `{"answer": "Add it to your config:\n  { \"aliases\": { \"id\": \"guid --copy\" } }"}`
	ai, _, project, run := aiFixture(t, true, answer)
	out, _, err := run("y\ny\n", "how do I make a new alias")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"aliases"`) || !strings.Contains(out, "guid --copy") {
		t.Errorf("the answer should be printed:\n%s", out)
	}
	if fileExists(filepath.Join(project, ".zx", "commands")) {
		t.Error("an answer must not save anything")
	}
	if len(ai.prompts) != 1 || !strings.Contains(ai.prompts[0], "zx config init") || !strings.Contains(ai.prompts[0], `{"answer": "..."}`) {
		t.Error("the prompt should tell the AI about zx and that it may answer")
	}

	// zx new with a question prints the answer, says why nothing was written, and saves nothing.
	_, _, project, run = aiFixture(t, true, answer)
	out, errOut, err := run("y\n", "new", "how do I make a new alias")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "guid --copy") || !strings.Contains(errOut, "no command was written") || fileExists(filepath.Join(project, ".zx", "commands")) {
		t.Errorf("zx new with a question: out=%q err=%q", out, errOut)
	}

	// An answer that also carries a script is treated as a command, not an answer.
	both := `{"answer": "here", "name": "mark", "summary": "Write a marker file", "language": "js", "script": "console.log(1)"}`
	_, _, _, run = aiFixture(t, true, both)
	out, errOut, err = run("n\nn\n", "write a marker file here")
	if err != nil || strings.Contains(out, "here") || !strings.Contains(errOut, "zx mark") {
		t.Errorf("a reply with a script should be shown as a command: out=%q err=%q %v", out, errOut, err)
	}
}

func TestAIErrorsSayHowToFixThem(t *testing.T) {
	cases := []struct{ provider, msg, want string }{
		{"claude", "claude: exit status 1: Failed to authenticate: OAuth session expired and could not be refreshed", "sign in again"},
		{"claude", `exec: "claude": executable file not found in $PATH`, "not installed"},
		{"ollama", "Post http://127.0.0.1:11434/api/generate: dial tcp 127.0.0.1:11434: connect: connection refused", "not running"},
		{"ollama", "404 Not Found: model 'llama9' not found", "ollama pull"},
		{"anthropic", "401 Unauthorized: invalid x-api-key", "rejected the key"},
		{"anthropic", "429 Too Many Requests: rate_limit_error", "rate limiting"},
		{"claude", "context deadline exceeded", "too long"},
		{"custom", "exit status 127: not found", "ai.command"},
	}
	for _, tc := range cases {
		err := askError(tc.provider, errors.New(tc.msg))
		if !strings.Contains(err.Error(), tc.msg) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s / %q: got %q, want a hint containing %q", tc.provider, tc.msg, err, tc.want)
		}
	}
	// Anything unknown still points somewhere useful.
	if err := askError("claude", errors.New("something odd")); !strings.Contains(err.Error(), "zx ai test") {
		t.Errorf("unknown errors should point to zx ai test: %v", err)
	}
}
