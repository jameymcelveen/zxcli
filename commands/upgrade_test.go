package commands

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jameymcelveen/zxcli/core"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"v0.1.0", "v0.2.0", -1, true},
		{"v0.2.0", "v0.2.0", 0, true},
		{"v0.10.0", "v0.9.9", 1, true},
		{"1.2.3", "v1.2.4", -1, true},
		{"v0.2.0-3-gabc123-dirty", "v0.2.0", 0, true},
		{"v2.0.0", "v1.99.99", 1, true},
		{"dev", "v0.2.0", 0, false},
		{"83abaff", "v0.2.0", 0, false},
	}
	for _, tc := range cases {
		if cmp, ok := CompareVersions(tc.a, tc.b); cmp != tc.cmp || ok != tc.ok {
			t.Errorf("CompareVersions(%s, %s) = %d, %v; want %d, %v", tc.a, tc.b, cmp, ok, tc.cmp, tc.ok)
		}
	}
}

// releaseArchive builds the archive a release would carry for this platform.
func releaseArchive(t *testing.T, binary string) (name string, data []byte) {
	t.Helper()
	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		for _, f := range [][2]string{{"zx.exe", binary}, {"LICENSE", "MIT"}} {
			w, err := zw.Create(f[0])
			if err != nil {
				t.Fatal(err)
			}
			w.Write([]byte(f[1]))
		}
		zw.Close()
		return fmt.Sprintf("zx_windows_%s.zip", runtime.GOARCH), buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range [][2]string{{"LICENSE", "MIT"}, {"zx", binary}} {
		tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o755, Size: int64(len(f[1])), Typeflag: tar.TypeReg})
		tw.Write([]byte(f[1]))
	}
	tw.Close()
	gz.Close()
	return fmt.Sprintf("zx_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH), buf.Bytes()
}

// fakeRelease serves a release tagged v9.9.9 and points the upgrade command
// at it and at a stand-in "installed" binary. It returns that binary's path.
func fakeRelease(t *testing.T, installed string, corruptChecksum bool) string {
	t.Helper()
	asset, archive := releaseArchive(t, "new binary")
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])
	if corruptChecksum {
		checksum = strings.Repeat("0", 64)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name": "v9.9.9", "name": "zx v9.9.9"}`)
	})
	mux.HandleFunc("/dl/v9.9.9/"+asset, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/dl/v9.9.9/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  some_other_file.deb\n%s  %s\n", strings.Repeat("a", 64), checksum, asset)
	})
	srv := httptest.NewServer(mux)

	exe := filepath.Join(t.TempDir(), "zx")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldAPI, oldDownload, oldTarget, oldVersion := upgradeAPI, upgradeDownload, upgradeTarget, core.Version
	upgradeAPI, upgradeDownload = srv.URL+"/api/latest", srv.URL+"/dl"
	upgradeTarget = func() (string, error) { return exe, nil }
	core.Version = installed
	t.Cleanup(func() {
		srv.Close()
		upgradeAPI, upgradeDownload, upgradeTarget, core.Version = oldAPI, oldDownload, oldTarget, oldVersion
	})
	return exe
}

func contents(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpgradeReplacesTheBinary(t *testing.T) {
	exe := fakeRelease(t, "v0.1.0", false)

	if out := mustRun(t, "", "upgrade", "--check"); !strings.Contains(out, "v9.9.9 is available (you have v0.1.0)") {
		t.Errorf("--check printed %q", out)
	}
	if contents(t, exe) != "old binary" {
		t.Fatal("--check must not change the binary")
	}

	if out := mustRun(t, "", "upgrade"); !strings.Contains(out, "upgraded v0.1.0 to v9.9.9") {
		t.Errorf("upgrade printed %q", out)
	}
	if got := contents(t, exe); got != "new binary" {
		t.Errorf("binary is %q after the upgrade", got)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(exe); st.Mode()&0o111 == 0 {
			t.Error("the new binary is not executable")
		}
	}
	if fileExists(exe + ".new") {
		t.Error("the temporary download was left behind")
	}
}

func TestUpgradeLeavesCurrentAndDevBuildsAlone(t *testing.T) {
	exe := fakeRelease(t, "v9.9.9", false)
	if out := mustRun(t, "", "upgrade"); !strings.Contains(out, "v9.9.9 is up to date") {
		t.Errorf("current build: %q", out)
	}
	core.Version = "v10.0.0"
	if out := mustRun(t, "", "upgrade"); !strings.Contains(out, "up to date") {
		t.Errorf("newer build: %q", out)
	}
	core.Version = "dev"
	if out := mustRun(t, "", "upgrade"); !strings.Contains(out, "development build") || !strings.Contains(out, "--force") {
		t.Errorf("dev build: %q", out)
	}
	if contents(t, exe) != "old binary" {
		t.Fatal("nothing should have been replaced")
	}
	if out := mustRun(t, "", "upgrade", "--force"); !strings.Contains(out, "upgraded dev to v9.9.9") || contents(t, exe) != "new binary" {
		t.Errorf("--force on a dev build: %q", out)
	}
}

func TestUpgradeRefusesABadChecksum(t *testing.T) {
	exe := fakeRelease(t, "v0.1.0", true)
	_, _, err := run(t, "", "upgrade")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected a checksum error, got %v", err)
	}
	if contents(t, exe) != "old binary" {
		t.Error("a failed upgrade must leave the binary alone")
	}
}

func TestUpgradeReportsNetworkTrouble(t *testing.T) {
	fakeRelease(t, "v0.1.0", false)
	upgradeAPI = "http://127.0.0.1:1/nothing-listens-here"
	if _, _, err := run(t, "", "upgrade"); err == nil || !strings.Contains(err.Error(), "can't check for updates") {
		t.Errorf("expected a friendly network error, got %v", err)
	}
}
