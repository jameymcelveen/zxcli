package commands

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jameymcelveen/zxcli/core"
)

func init() { core.Register("serve", runServe) }

func runServe(c *core.Context) error {
	fs := c.Flags()
	port := fs.Int("p", 8080, "port to listen on")
	lan := fs.Bool("lan", false, "let other devices on your network connect")
	pos, err := c.Parse(fs)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) > 0 {
		dir = pos[0]
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return core.Usagef("%q is not a folder", dir)
	}
	host := "127.0.0.1"
	if *lan {
		host = "0.0.0.0"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(*port)))
	if err != nil {
		return fmt.Errorf("can't listen on port %d: %w (see what has it with: zx port %d)", *port, err, *port)
	}
	c.Printf("serving %s\n  http://localhost:%d\n", dir, *port)
	if *lan {
		if addrs, _ := localAddrs(false); len(addrs) > 0 {
			c.Printf("  http://%s:%d\n", addrs[0][1], *port)
		}
	}
	c.Println("press Ctrl+C to stop")
	return http.Serve(ln, serveHandler(dir, c.Stderr))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// serveHandler serves files from dir and logs one line per request.
func serveHandler(dir string, log io.Writer) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(sw, r)
		fmt.Fprintf(log, "%s  %d  %s %s\n", time.Now().Format("15:04:05"), sw.status, r.Method, r.URL.Path)
	})
}
