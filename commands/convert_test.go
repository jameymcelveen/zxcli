package commands

import (
	"strings"
	"testing"
	"time"
)

func TestNowConvertsEpoch(t *testing.T) {
	out := mustRun(t, "", "now", "0")
	for _, want := range []string{"iso     1970-01-01T00:00:00Z", "epoch   0", "millis  0"} {
		if !strings.Contains(out, want) {
			t.Errorf("zx now 0 is missing %q in:\n%s", want, out)
		}
	}
	if got := mustRun(t, "", "now", "1700000000", "-f", "iso"); got != "2023-11-14T22:13:20Z" {
		t.Errorf("seconds: got %s", got)
	}
	if got := mustRun(t, "", "now", "1700000000000", "-f", "iso"); got != "2023-11-14T22:13:20Z" {
		t.Errorf("milliseconds: got %s", got)
	}
	if got := mustRun(t, "", "now", "2023-11-14T22:13:20Z", "-f", "epoch"); got != "1700000000" {
		t.Errorf("date to epoch: got %s", got)
	}
}

func TestNowCurrentTimeAndErrors(t *testing.T) {
	got := mustRun(t, "", "epoch", "-f", "epoch") // "epoch" is an alias
	if len(got) != 10 {
		t.Errorf("current epoch looks wrong: %s", got)
	}
	if _, _, err := run(t, "", "now", "next tuesday"); err == nil {
		t.Error("unparseable dates should fail")
	}
	if _, _, err := run(t, "", "now", "-f", "nope"); err == nil {
		t.Error("unknown fields should fail")
	}
}

func TestParseWhenUsesLocation(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database")
	}
	got, err := ParseWhen("2026-11-18 10:00", ny)
	if err != nil {
		t.Fatal(err)
	}
	if got.UTC().Hour() != 15 {
		t.Errorf("10:00 in New York should be 15:00 UTC, got %s", got.UTC())
	}
}

func TestB64(t *testing.T) {
	if got := mustRun(t, "", "b64", "hello"); got != "aGVsbG8=" {
		t.Errorf("encode: got %s", got)
	}
	if got := mustRun(t, "", "b64", "-d", "aGVsbG8="); got != "hello" {
		t.Errorf("decode: got %s", got)
	}
	if got := mustRun(t, "", "b64", "-d", "aGVsbG8"); got != "hello" {
		t.Errorf("decode without padding: got %s", got)
	}
	if got := mustRun(t, "piped", "b64"); got != "cGlwZWQ=" {
		t.Errorf("stdin: got %s", got)
	}
	if got := mustRun(t, "", "b64", "-u", "??>>"); got != "Pz8-Pg==" {
		t.Errorf("url-safe: got %s", got)
	}
	if got := mustRun(t, "", "b64", "-d", "Pz8-Pg=="); got != "??>>" {
		t.Errorf("url-safe decode: got %s", got)
	}
	if _, _, err := run(t, "", "b64", "-d", "!!!"); err == nil {
		t.Error("invalid base64 should fail")
	}
	if _, _, err := run(t, "", "b64"); err == nil {
		t.Error("no input on a terminal should fail")
	}
}

func TestURL(t *testing.T) {
	if got := mustRun(t, "", "url", "a b&c=d/é"); got != "a%20b%26c%3Dd%2F%C3%A9" {
		t.Errorf("encode: got %s", got)
	}
	if got := mustRun(t, "", "url", "-d", "a%20b%26c+d"); got != "a b&c d" {
		t.Errorf("decode: got %s", got)
	}
	out := mustRun(t, "", "url", "-p", "https://me@example.com:8443/a/b?x=1&y=two#top")
	for _, want := range []string{"scheme    https", "user      me", "host      example.com", "port      8443", "path      /a/b", "fragment  top", "query     x = 1", "query     y = two"} {
		if !strings.Contains(out, want) {
			t.Errorf("parse is missing %q in:\n%s", want, out)
		}
	}
}

func TestCase(t *testing.T) {
	cases := []struct{ style, in, want string }{
		{"camel", "hello world", "helloWorld"},
		{"pascal", "my-var-name", "MyVarName"},
		{"snake", "HelloWorld", "hello_world"},
		{"snake", "XMLHttpRequest", "xml_http_request"},
		{"kebab", "some_snake_value", "some-snake-value"},
		{"const", "maxRetryCount", "MAX_RETRY_COUNT"},
		{"title", "the quick_brown-fox", "The Quick Brown Fox"},
		{"snake", "version2Beta", "version2_beta"},
	}
	for _, tc := range cases {
		if got := mustRun(t, "", "case", tc.style, tc.in); got != tc.want {
			t.Errorf("case %s %q = %q, want %q", tc.style, tc.in, got, tc.want)
		}
	}
	all := mustRun(t, "", "case", "hello world")
	if !strings.Contains(all, "camel   helloWorld") || !strings.Contains(all, "const   HELLO_WORLD") {
		t.Errorf("listing all styles printed:\n%s", all)
	}
	// A single word that happens to be a style name is still just text.
	if got := mustRun(t, "", "case", "snake"); !strings.Contains(got, "pascal  Snake") {
		t.Errorf("single word: got\n%s", got)
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Hello, World!":            "hello-world",
		"  Don't  Panic -- ever  ": "dont-panic-ever",
		"Crème brûlée №5":          "crème-brûlée-5",
		"already-a-slug":           "already-a-slug",
		"C# & .NET 9: What's New?": "c-net-9-whats-new",
	}
	for in, want := range cases {
		if got := mustRun(t, "", "slug", in); got != want {
			t.Errorf("slug %q = %q, want %q", in, got, want)
		}
	}
}
