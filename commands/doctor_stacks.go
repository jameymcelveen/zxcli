package commands

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/jameymcelveen/zxcli/core"
)

// A stack is a ready-made set of doctor checks for a kind of project. It
// reads the project's own files for the versions it needs, so "laravel"
// asks for the PHP version composer.json asks for.
type stackDef struct {
	Name    string
	Summary string
	Aliases []string
	// Detect says why a folder looks like this stack, or "" when it does not.
	Detect func(dir string) string
	// Checks lists what the stack needs. rel is the folder relative to the
	// project root ("" for the root itself), used to make fixes run there.
	Checks func(dir, rel string) []core.DoctorCheck
}

var stacks = []stackDef{
	{Name: "dotnet", Summary: ".NET SDK, at the version the solution targets", Aliases: []string{".net", "csharp", "fsharp"}, Detect: detectDotnet, Checks: dotnetChecks},
	{Name: "node", Summary: "Node.js, your package manager, node_modules installed", Aliases: []string{"nodejs", "js", "javascript", "typescript"}, Detect: func(d string) string { return ifFile(d, "package.json") }, Checks: nodeChecks},
	{Name: "react", Summary: "a React app: Node.js, package manager, node_modules", Detect: depDetect("react"), Checks: nodeChecks},
	{Name: "next", Summary: "a Next.js app: Node.js, package manager, node_modules", Aliases: []string{"nextjs"}, Detect: depDetect("next"), Checks: nodeChecks},
	{Name: "vue", Summary: "a Vue app: Node.js, package manager, node_modules", Aliases: []string{"vuejs"}, Detect: depDetect("vue"), Checks: nodeChecks},
	{Name: "angular", Summary: "an Angular app: Node.js, package manager, node_modules", Detect: depDetect("@angular/core"), Checks: nodeChecks},
	{Name: "laravel", Summary: "PHP, Composer, vendor/ installed, .env present, plus Node when there is a package.json", Detect: detectLaravel, Checks: laravelChecks},
	{Name: "php", Summary: "PHP, Composer, vendor/ installed", Detect: func(d string) string { return ifFile(d, "composer.json") }, Checks: phpChecks},
	{Name: "python", Summary: "Python 3, at the version the project asks for", Aliases: []string{"py", "django", "flask", "fastapi"}, Detect: detectPython, Checks: pythonChecks},
	{Name: "go", Summary: "Go, at the version go.mod asks for", Aliases: []string{"golang"}, Detect: func(d string) string { return ifFile(d, "go.mod") }, Checks: goChecks},
	{Name: "rust", Summary: "cargo and rustc, at the version Cargo.toml asks for", Aliases: []string{"cargo"}, Detect: func(d string) string { return ifFile(d, "Cargo.toml") }, Checks: rustChecks},
	{Name: "java", Summary: "a JDK, plus Maven or Gradle when there is no wrapper", Aliases: []string{"maven", "gradle", "kotlin", "spring"}, Detect: detectJava, Checks: javaChecks},
	{Name: "ruby", Summary: "Ruby and Bundler", Detect: func(d string) string { return ifFile(d, "Gemfile") }, Checks: rubyChecks},
	{Name: "rails", Summary: "Ruby and Bundler for a Rails app", Aliases: []string{"ror"}, Detect: detectRails, Checks: rubyChecks},
	{Name: "docker", Summary: "Docker, when there is a Dockerfile or compose file", Aliases: []string{"compose"}, Detect: detectDocker, Checks: func(string, string) []core.DoctorCheck { return []core.DoctorCheck{{Tool: "docker"}} }},
	{Name: "make", Summary: "make, when there is a Makefile", Detect: func(d string) string { return ifFile(d, "Makefile") }, Checks: func(string, string) []core.DoctorCheck { return []core.DoctorCheck{{Tool: "make"}} }},
}

func findStack(name string) *stackDef {
	name = strings.ToLower(strings.TrimSpace(name))
	for i := range stacks {
		if stacks[i].Name == name {
			return &stacks[i]
		}
		for _, a := range stacks[i].Aliases {
			if a == name {
				return &stacks[i]
			}
		}
	}
	return nil
}

func stackNames() string {
	var names []string
	for _, s := range stacks {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

func printStacks(c *core.Context) {
	nameW := 0
	for _, s := range stacks {
		nameW = max(nameW, len(s.Name))
	}
	c.Println("Stacks you can add: zx doctor laravel react   (name@folder for a subfolder, e.g. react@web)")
	c.Println()
	for _, s := range stacks {
		c.Printf("  %s  %s\n", padRight(s.Name, nameW), s.Summary)
	}
	c.Println("\nOr let zx look at this folder: zx doctor --discover")
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// baseDir is the project root when there is one, otherwise the current folder.
func baseDir(cfg core.Config, cwd string) string {
	if cfg.ProjectDir != "" {
		return cfg.ProjectDir
	}
	return cwd
}

// StackChecks turns specs like "laravel" or "react@web" into checks.
func StackChecks(base string, specs []string) ([]core.DoctorCheck, error) {
	var out []core.DoctorCheck
	for _, spec := range specs {
		name, sub, _ := strings.Cut(spec, "@")
		st := findStack(name)
		if st == nil {
			return nil, core.Usagef("unknown stack %q. Known stacks: %s", name, stackNames())
		}
		out = mergeChecks(out, stackFor(st, base, sub))
	}
	return out, nil
}

// stackFor builds one stack's checks for the folder base/sub.
func stackFor(st *stackDef, base, sub string) []core.DoctorCheck {
	dir := filepath.Join(base, sub)
	checks := st.Checks(dir, filepath.ToSlash(sub))
	for i := range checks {
		checks[i].Stack, checks[i].Dir = st.Name, dir
	}
	return checks
}

// mergeChecks appends more to list. The same tool twice keeps the higher
// minimum version; the same file twice is kept once.
func mergeChecks(list, more []core.DoctorCheck) []core.DoctorCheck {
	for _, m := range more {
		dup := false
		for i, have := range list {
			switch {
			case m.Tool != "" && have.Tool == m.Tool:
				dup = true
				if m.Min != "" && (have.Min == "" || VersionAtLeast(m.Min, have.Min)) {
					list[i].Min = m.Min
				}
			case m.File != "" && have.File == m.File && have.Dir == m.Dir:
				dup = true
			}
		}
		if !dup {
			list = append(list, m)
		}
	}
	return list
}

// ---------------------------------------------------------------- detection

// foundStack is a stack discovery recognised in a folder.
type foundStack struct {
	stack  *stackDef
	rel    string // "" for the root folder
	why    string
	checks []core.DoctorCheck
}

func (f foundStack) spec() string {
	if f.rel == "" {
		return f.stack.Name
	}
	return f.stack.Name + "@" + f.rel
}

var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true, "bin": true, "obj": true, "dist": true, "build": true,
	".venv": true, "venv": true, "target": true, ".next": true, "__pycache__": true, ".zx": true, ".idea": true, ".vscode": true,
}

// findStacks looks at dir and its immediate subfolders.
func findStacks(dir string) []foundStack {
	var out []foundStack
	scan := func(rel string) {
		path := filepath.Join(dir, rel)
		var here []foundStack
		for i := range stacks {
			if why := stacks[i].Detect(path); why != "" {
				here = append(here, foundStack{stack: &stacks[i], rel: filepath.ToSlash(rel), why: why})
			}
		}
		// A framework says more than the language under it.
		has := map[string]bool{}
		for _, f := range here {
			has[f.stack.Name] = true
		}
		hidden := map[string]bool{}
		if has["react"] || has["next"] || has["vue"] || has["angular"] {
			hidden["node"] = true
		}
		if has["next"] {
			hidden["react"] = true
		}
		if has["laravel"] {
			hidden["php"] = true
		}
		if has["rails"] {
			hidden["ruby"] = true
		}
		for _, f := range here {
			if !hidden[f.stack.Name] {
				f.checks = stackFor(f.stack, dir, f.rel)
				out = append(out, f)
			}
		}
	}
	scan("")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && !skipDirs[e.Name()] && !strings.HasPrefix(e.Name(), ".") {
			scan(e.Name())
		}
	}
	return out
}

func ifFile(dir string, names ...string) string {
	for _, n := range names {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && !st.IsDir() {
			return n
		}
	}
	return ""
}

func readText(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || len(data) > 2<<20 {
		return ""
	}
	return string(data)
}

func readJSONFile(dir, name string, into any) bool {
	data := readText(dir, name)
	return data != "" && json.Unmarshal([]byte(data), into) == nil
}

type packageJSON struct {
	Dependencies    map[string]json.RawMessage `json:"dependencies"`
	DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	Engines         map[string]string          `json:"engines"`
}

func depDetect(dep string) func(string) string {
	return func(dir string) string {
		var p packageJSON
		if readJSONFile(dir, "package.json", &p) {
			_, a := p.Dependencies[dep]
			_, b := p.DevDependencies[dep]
			if a || b {
				return "package.json lists " + dep
			}
		}
		return ""
	}
}

type composerJSON struct {
	Require    map[string]string `json:"require"`
	RequireDev map[string]string `json:"require-dev"`
}

func detectLaravel(dir string) string {
	var c composerJSON
	if readJSONFile(dir, "composer.json", &c) {
		if _, ok := c.Require["laravel/framework"]; ok {
			return "composer.json requires laravel/framework"
		}
	}
	if ifFile(dir, "artisan") != "" && ifFile(dir, "composer.json") != "" {
		return "artisan and composer.json"
	}
	return ""
}

func detectDotnet(dir string) string {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".sln", ".slnx", ".csproj", ".fsproj":
			if !e.IsDir() {
				return e.Name()
			}
		}
	}
	return ifFile(dir, "global.json")
}

func detectPython(dir string) string {
	return ifFile(dir, "pyproject.toml", "requirements.txt", "Pipfile", "setup.py", ".python-version")
}

func detectJava(dir string) string {
	return ifFile(dir, "pom.xml", "build.gradle", "build.gradle.kts")
}

func detectRails(dir string) string {
	if ifFile(dir, "Gemfile") != "" && regexp.MustCompile(`(?m)^\s*gem\s+['"]rails['"]`).MatchString(readText(dir, "Gemfile")) {
		return "Gemfile uses rails"
	}
	return ""
}

func detectDocker(dir string) string {
	return ifFile(dir, "Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml")
}

// ------------------------------------------------------------------ versions

var anyVersion = regexp.MustCompile(`\d+(?:\.\d+)*`)

// firstVersion pulls the first version number out of a requirement such as
// "^8.2", ">=20 <23" or "v18.17.0". It returns "" when there is none.
func firstVersion(spec string) string {
	return anyVersion.FindString(spec)
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

func match1(re, text string) string {
	if m := regexp.MustCompile(re).FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

func dotnetMin(dir string) string {
	var g struct {
		SDK struct {
			Version string `json:"version"`
		} `json:"sdk"`
	}
	if readJSONFile(dir, "global.json", &g) {
		if v := firstVersion(g.SDK.Version); v != "" {
			return strings.SplitN(v, ".", 2)[0]
		}
	}
	best := 0
	re := regexp.MustCompile(`\bnet(\d+)\.\d+`)
	root := dir
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.Count(strings.TrimPrefix(path, root), string(filepath.Separator)) > 3) {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".csproj" || ext == ".fsproj" {
			if data, err := os.ReadFile(path); err == nil {
				for _, m := range re.FindAllStringSubmatch(string(data), -1) {
					if n, _ := strconv.Atoi(m[1]); n > best {
						best = n
					}
				}
			}
		}
		return nil
	})
	if best > 0 {
		return strconv.Itoa(best)
	}
	return ""
}

func nodeMin(dir string) string {
	for _, f := range []string{".nvmrc", ".node-version"} {
		if v := firstVersion(firstLineOf(readText(dir, f))); v != "" {
			return v
		}
	}
	var p packageJSON
	if readJSONFile(dir, "package.json", &p) {
		return firstVersion(p.Engines["node"])
	}
	return ""
}

func phpMin(dir string) string {
	var c composerJSON
	if readJSONFile(dir, "composer.json", &c) {
		return firstVersion(c.Require["php"])
	}
	return ""
}

func pythonMin(dir string) string {
	if v := firstVersion(firstLineOf(readText(dir, ".python-version"))); v != "" {
		return v
	}
	return firstVersion(match1(`(?m)^\s*requires-python\s*=\s*["']([^"']+)["']`, readText(dir, "pyproject.toml")))
}

func javaMin(dir string) string {
	v := match1(`<(?:maven\.compiler\.release|maven\.compiler\.source|java\.version|release)>\s*(\d+(?:\.\d+)?)\s*<`, readText(dir, "pom.xml"))
	for _, f := range []string{"build.gradle", "build.gradle.kts"} {
		if v == "" {
			text := readText(dir, f)
			v = firstNonEmpty(match1(`JavaLanguageVersion\.of\((\d+)\)`, text), match1(`sourceCompatibility\s*=\s*['"]?(?:JavaVersion\.VERSION_)?(\d+(?:[._]\d+)?)`, text))
		}
	}
	v = strings.ReplaceAll(v, "_", ".")
	if strings.HasPrefix(v, "1.") {
		v = strings.TrimPrefix(v, "1.")
	}
	return v
}

func rubyMin(dir string) string {
	if v := firstVersion(firstLineOf(readText(dir, ".ruby-version"))); v != "" {
		return v
	}
	return match1(`(?m)^\s*ruby\s+['"](\d+(?:\.\d+)*)['"]`, readText(dir, "Gemfile"))
}

// ------------------------------------------------------------------- checks

// inDir makes a fix command run in a subfolder of the project.
func inDir(rel, cmd string) string {
	if rel == "" {
		return cmd
	}
	return "cd " + rel + " && " + cmd
}

func allOS(cmd string) core.OSCommands {
	return core.OSCommands{"darwin": cmd, "linux": cmd, "windows": cmd}
}

func fileCheck(rel, name, file, fix string) core.DoctorCheck {
	label := name
	if rel != "" {
		label = rel + "/" + name
	}
	d := core.DoctorCheck{Name: label, File: file}
	if fix != "" {
		d.Install = allOS(inDir(rel, fix))
	}
	return d
}

func dotnetChecks(dir, rel string) []core.DoctorCheck {
	return []core.DoctorCheck{{Tool: "dotnet", Min: dotnetMin(dir)}}
}

func nodeChecks(dir, rel string) []core.DoctorCheck {
	pm, install := "npm", "npm install"
	switch {
	case ifFile(dir, "pnpm-lock.yaml") != "":
		pm, install = "pnpm", "pnpm install"
	case ifFile(dir, "yarn.lock") != "":
		pm, install = "yarn", "yarn install"
	case ifFile(dir, "bun.lockb", "bun.lock") != "":
		pm, install = "bun", "bun install"
	case ifFile(dir, "package-lock.json") != "":
		install = "npm ci"
	}
	checks := []core.DoctorCheck{{Tool: "node", Min: nodeMin(dir)}, {Tool: "npm"}}
	if pm != "npm" {
		pmCheck := core.DoctorCheck{Tool: pm}
		if pm == "pnpm" || pm == "yarn" {
			pmCheck.Install = allOS("npm install -g " + pm)
		}
		checks = append(checks, pmCheck)
	}
	if ifFile(dir, "package.json") != "" {
		checks = append(checks, fileCheck(rel, "node_modules", "node_modules", install))
	}
	return checks
}

func phpChecks(dir, rel string) []core.DoctorCheck {
	return []core.DoctorCheck{
		{Tool: "php", Min: phpMin(dir)},
		{Tool: "composer"},
		fileCheck(rel, "vendor", "vendor", "composer install"),
	}
}

func laravelChecks(dir, rel string) []core.DoctorCheck {
	checks := phpChecks(dir, rel)
	if ifFile(dir, ".env.example") != "" {
		cp := "cp .env.example .env"
		if runtime.GOOS == "windows" {
			cp = "copy .env.example .env"
		}
		checks = append(checks, fileCheck(rel, ".env", ".env", cp))
	}
	if ifFile(dir, "package.json") != "" {
		checks = append(checks, nodeChecks(dir, rel)...)
	}
	return checks
}

func pythonChecks(dir, rel string) []core.DoctorCheck {
	return []core.DoctorCheck{{Tool: "python3", Min: pythonMin(dir)}}
}

func goChecks(dir, rel string) []core.DoctorCheck {
	return []core.DoctorCheck{{Tool: "go", Min: match1(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)`, readText(dir, "go.mod"))}}
}

func rustChecks(dir, rel string) []core.DoctorCheck {
	return []core.DoctorCheck{
		{Tool: "cargo"},
		{Tool: "rustc", Min: match1(`(?m)^\s*rust-version\s*=\s*"(\d+(?:\.\d+)*)"`, readText(dir, "Cargo.toml"))},
	}
}

func javaChecks(dir, rel string) []core.DoctorCheck {
	checks := []core.DoctorCheck{{Tool: "java", Min: javaMin(dir)}}
	if ifFile(dir, "pom.xml") != "" && ifFile(dir, "mvnw", "mvnw.cmd") == "" {
		checks = append(checks, core.DoctorCheck{Tool: "mvn"})
	}
	if ifFile(dir, "build.gradle", "build.gradle.kts") != "" && ifFile(dir, "gradlew", "gradlew.bat") == "" {
		checks = append(checks, core.DoctorCheck{Tool: "gradle"})
	}
	return checks
}

func rubyChecks(dir, rel string) []core.DoctorCheck {
	return []core.DoctorCheck{
		{Tool: "ruby", Min: rubyMin(dir)},
		{Tool: "bundle", Install: allOS("gem install bundler")},
	}
}

// stackNamesOf lists the distinct stack names in specs, for messages.
func stackNamesOf(found []foundStack) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range found {
		if !seen[f.stack.Name] {
			seen[f.stack.Name] = true
			out = append(out, f.stack.Name)
		}
	}
	sort.Strings(out)
	return out
}
