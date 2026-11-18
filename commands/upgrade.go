package commands

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("upgrade", runUpgrade) }

// Where releases come from. Tests point these at a local server.
var (
	upgradeAPI      = "https://api.github.com/repos/jameymcelveen/zxcli/releases/latest"
	upgradeDownload = "https://github.com/jameymcelveen/zxcli/releases/download"
	upgradeTarget   = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(exe)
	}
)

const maxDownload = 200 << 20

func runUpgrade(c *core.Context) error {
	flags := c.Flags()
	checkOnly := flags.Bool("check", false, "only say whether a newer version exists")
	force := flags.Bool("force", false, "reinstall the latest release even if this build looks current")
	if _, err := c.Parse(flags); err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	latest, err := latestRelease(client)
	if err != nil {
		return fmt.Errorf("can't check for updates: %w", err)
	}
	current := core.Version
	cmp, known := CompareVersions(current, latest)
	switch {
	case !known && !*force:
		c.Printf("this is a development build (%s). The latest release is %s.\n", current, latest)
		c.Println("run 'zx upgrade --force' to replace it with the release")
		return nil
	case known && cmp >= 0 && !*force:
		c.Printf("zx %s is up to date\n", current)
		return nil
	case *checkOnly:
		c.Printf("zx %s is available (you have %s). Run: zx upgrade\n", latest, current)
		return nil
	}

	exe, err := upgradeTarget()
	if err != nil {
		return err
	}
	c.Printf("downloading zx %s\n", latest)
	bin, err := downloadRelease(client, latest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := replaceExecutable(exe, bin); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("no permission to replace %s. If a package installed it, try: sudo zx upgrade", exe)
		}
		return err
	}
	c.Printf("upgraded %s to %s\n", current, latest)
	return nil
}

var versionTriple = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// CompareVersions returns -1, 0 or 1 as a is older than, the same as, or
// newer than b. ok is false when either is not a version number, such as the
// "dev" of a local build.
func CompareVersions(a, b string) (cmp int, ok bool) {
	ma, mb := versionTriple.FindStringSubmatch(a), versionTriple.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return 0, false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			if x < y {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func httpGet(client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "zx/"+core.Version)
	req.Header.Set("Accept", "application/vnd.github+json, */*")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, maxDownload))
}

func latestRelease(client *http.Client) (string, error) {
	body, err := httpGet(client, upgradeAPI)
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.TagName == "" {
		return "", errors.New("the release list came back in a form zx does not understand")
	}
	return rel.TagName, nil
}

// downloadRelease fetches the archive for one platform, checks it against the
// release checksums, and returns the zx binary inside it.
func downloadRelease(client *http.Client, tag, goos, goarch string) ([]byte, error) {
	asset := fmt.Sprintf("zx_%s_%s.tar.gz", goos, goarch)
	if goos == "windows" {
		asset = fmt.Sprintf("zx_%s_%s.zip", goos, goarch)
	}
	base := upgradeDownload + "/" + tag + "/"
	archive, err := httpGet(client, base+asset)
	if err != nil {
		return nil, err
	}
	sums, err := httpGet(client, base+"checksums.txt")
	if err != nil {
		return nil, err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == asset {
			want = f[0]
		}
	}
	got := sha256.Sum256(archive)
	if want == "" || want != hex.EncodeToString(got[:]) {
		return nil, fmt.Errorf("checksum mismatch for %s, so nothing was installed", asset)
	}
	if goos == "windows" {
		return fromZip(archive, "zx.exe")
	}
	return fromTarGz(archive, "zx")
}

func fromTarGz(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("%s is not in the download", name)
		}
		if filepath.Base(h.Name) == name && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

func fromZip(archive []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxDownload))
		}
	}
	return nil, fmt.Errorf("%s is not in the download", name)
}

// replaceExecutable swaps the file at exe for data. The new file is written
// beside the old one and renamed into place, so a failure part-way leaves the
// working binary alone. Windows cannot overwrite a running program, so there
// the old one is moved aside first.
func replaceExecutable(exe string, data []byte) error {
	fresh, old := exe+".new", exe+".old"
	if err := os.WriteFile(fresh, data, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			os.Remove(fresh)
			return err
		}
		if err := os.Rename(fresh, exe); err != nil {
			os.Rename(old, exe)
			os.Remove(fresh)
			return err
		}
		return nil
	}
	if err := os.Rename(fresh, exe); err != nil {
		os.Remove(fresh)
		return err
	}
	return nil
}
