package commands

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSON(t *testing.T) {
	pretty := mustRun(t, `{"b":1,"a":[true,null]}`, "json")
	want := "{\n  \"b\": 1,\n  \"a\": [\n    true,\n    null\n  ]\n}"
	if pretty != want {
		t.Errorf("pretty-print:\n%s\nwant:\n%s", pretty, want)
	}
	if got := mustRun(t, "{\n \"b\" : 1,\n \"a\" : [ 1, 2 ]\n}", "json", "-m"); got != `{"b":1,"a":[1,2]}` {
		t.Errorf("minify: got %s", got)
	}
	file := filepath.Join(t.TempDir(), "x.json")
	if err := os.WriteFile(file, []byte(`{"k":"v"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, "", "json", file, "--indent", "4"); got != "{\n    \"k\": \"v\"\n}" {
		t.Errorf("file with --indent 4: got %s", got)
	}
	_, _, err := run(t, "{\n\"a\": 1,\n\"b\": oops\n}", "json")
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("invalid JSON should name the line, got %v", err)
	}
}

func makeJWT(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc([]byte(payload)) + ".c2lnbmF0dXJl"
}

func TestJWT(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).Unix()
	out, errOut, err := run(t, "", "jwt", makeJWT(fmt.Sprintf(`{"sub":"jamey","exp":%d}`, exp)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"alg": "HS256"`, `"sub": "jamey"`, "exp ", "(in 1h 59m)"} {
		if !strings.Contains(out, want) {
			t.Errorf("jwt output is missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "not verified") {
		t.Error("jwt should say the signature was not verified")
	}

	old := mustRun(t, "Bearer "+makeJWT(`{"exp":1700000000}`)+"\n", "jwt")
	if !strings.Contains(old, "EXPIRED") {
		t.Errorf("an old token should be marked expired:\n%s", old)
	}
	if _, _, err := run(t, "", "jwt", "not.a-jwt"); err == nil {
		t.Error("a malformed token should fail")
	}
}

func TestHash(t *testing.T) {
	const helloSHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := mustRun(t, "", "hash", "-s", "hello"); got != helloSHA256 {
		t.Errorf("sha256 of text: got %s", got)
	}
	if got := mustRun(t, "hello", "hash"); got != helloSHA256 {
		t.Errorf("sha256 of stdin: got %s", got)
	}
	if got := mustRun(t, "", "hash", "-a", "md5", "-s", "hello"); got != "5d41402abc4b2a76b9719d911017c592" {
		t.Errorf("md5: got %s", got)
	}
	if got := mustRun(t, "", "hash", "-a", "sha1", "-s", "hello"); got != "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d" {
		t.Errorf("sha1: got %s", got)
	}
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, "", "hash", file); got != helloSHA256+"  "+file {
		t.Errorf("file: got %s", got)
	}
	if _, _, err := run(t, "", "hash", "-a", "crc32", "-s", "x"); err == nil {
		t.Error("unknown algorithms should fail")
	}
	if _, _, err := run(t, "", "hash", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing files should fail")
	}
}
