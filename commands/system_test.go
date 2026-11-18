package commands

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseLsof(t *testing.T) {
	out := `COMMAND     PID          USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME
node      12345 jameymcelveen   23u  IPv4 0x1234567890abcdef      0t0  TCP *:3000 (LISTEN)
node      12345 jameymcelveen   24u  IPv6 0x1234567890abcdee      0t0  TCP *:3000 (LISTEN)
Google\x20Chrome 777 jameymcelveen 30u IPv4 0xabc 0t0 TCP 127.0.0.1:9222 (LISTEN)
postgres    501      postgres    7u  IPv6 0xdef      0t0  TCP [::1]:5432 (LISTEN)
`
	want := []Listener{
		{Port: 3000, PID: 12345, Proc: "node", User: "jameymcelveen"},
		{Port: 9222, PID: 777, Proc: "Google Chrome", User: "jameymcelveen"},
		{Port: 5432, PID: 501, Proc: "postgres", User: "postgres"},
	}
	if got := parseLsof(out); !reflect.DeepEqual(got, want) {
		t.Errorf("parseLsof:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseSS(t *testing.T) {
	out := `LISTEN 0      511                *:3000             *:*    users:(("node",pid=4242,fd=20))
LISTEN 0      4096       127.0.0.53%lo:53           0.0.0.0:*
LISTEN 0      128             [::]:22              [::]:*    users:(("sshd",pid=900,fd=4))
`
	want := []Listener{
		{Port: 3000, PID: 4242, Proc: "node"},
		{Port: 53, Proc: "?"},
		{Port: 22, PID: 900, Proc: "sshd"},
	}
	if got := parseSS(out); !reflect.DeepEqual(got, want) {
		t.Errorf("parseSS:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseNetstat(t *testing.T) {
	out := "\r\nActive Connections\r\n\r\n  Proto  Local Address          Foreign Address        State           PID\r\n" +
		"  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1040\r\n" +
		"  TCP    127.0.0.1:3000         0.0.0.0:0              LISTENING       8812\r\n" +
		"  TCP    192.168.1.5:50123      52.1.2.3:443           ESTABLISHED     8812\r\n" +
		"  TCP    [::]:3000              [::]:0                 LISTENING       8812\r\n"
	tasks := "\"svchost.exe\",\"1040\",\"Services\",\"0\",\"9,000 K\"\r\n\"node.exe\",\"8812\",\"Console\",\"1\",\"50,000 K\"\r\n"
	want := []Listener{
		{Port: 135, PID: 1040, Proc: "svchost.exe"},
		{Port: 3000, PID: 8812, Proc: "node.exe"},
	}
	if got := parseNetstat(out, tasks); !reflect.DeepEqual(got, want) {
		t.Errorf("parseNetstat:\n got %+v\nwant %+v", got, want)
	}
}

func TestPortFindsARealListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("can't open a listener here")
	}
	defer ln.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	out, _, err := run(t, "", "port", port)
	if err != nil {
		t.Skipf("no port lookup tool on this machine: %v", err)
	}
	if !strings.Contains(out, port) || strings.Contains(out, "nothing is listening") {
		t.Errorf("zx port %s did not find the test listener:\n%s", port, out)
	}
}

func TestPortValidation(t *testing.T) {
	for _, args := range [][]string{{"port", "99999"}, {"port", "http"}, {"port", "-k"}} {
		if _, _, err := run(t, "", args...); err == nil {
			t.Errorf("zx %s should fail", strings.Join(args, " "))
		}
	}
}

func TestIP(t *testing.T) {
	out, _, err := run(t, "", "ip", "-1")
	if err != nil {
		t.Skipf("no network interface here: %v", err)
	}
	if ip := net.ParseIP(strings.TrimSpace(out)); ip == nil || ip.To4() == nil {
		t.Errorf("zx ip -1 printed %q, want an IPv4 address", out)
	}
}

func TestServeHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi there"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	srv := httptest.NewServer(serveHandler(dir, &log))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != "hi there" {
		t.Errorf("GET /hello.txt = %d %q", res.StatusCode, body)
	}
	if res, err = http.Get(srv.URL + "/missing"); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("GET /missing = %d, want 404", res.StatusCode)
	}
	if l := log.String(); !strings.Contains(l, "200  GET /hello.txt") || !strings.Contains(l, "404  GET /missing") {
		t.Errorf("request log:\n%s", l)
	}
	if _, _, err := run(t, "", "serve", filepath.Join(dir, "nope")); err == nil {
		t.Error("serving a missing folder should fail")
	}
}
