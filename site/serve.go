package site

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ServerOptions configures the public HTTP surface.
type ServerOptions struct {
	Addr      string // listen address, e.g. "0.0.0.0:9000"
	PublicDir string // the ONLY directory served
	Token     string // optional shared secret; empty means fully public
}

// Serve exposes the generated site over HTTP until the process is signalled.
//
// The public surface is deliberately minimal: static files from PublicDir, no
// directory listing, no methods beyond GET/HEAD, and no path that can escape
// the root. The repository, data/ and any .env must never be reachable from
// here, so PublicDir is treated as the only trusted root.
func Serve(opts ServerOptions) error {
	handler, root, err := newStaticHandler(opts.PublicDir, opts.Token)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           withGzip(handler),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          log.New(os.Stderr, "[Site] http: ", log.LstdFlags),
	}

	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", opts.Addr, err)
	}
	log.Printf("[Site] 监听 %s（根目录 %s，鉴权=%v）", ln.Addr(), root, opts.Token != "")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		log.Printf("[Site] 收到退出信号，优雅关闭…")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newStaticHandler builds the whole request policy for the public site.
// It is separate from Serve so tests can exercise the security rules directly.
func newStaticHandler(publicDir, token string) (http.Handler, string, error) {
	root, err := filepath.Abs(publicDir)
	if err != nil {
		return nil, "", fmt.Errorf("解析 public 目录: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, "", fmt.Errorf("public 目录不可用（先跑 -mode site 生成）: %s", root)
	}
	fsys := os.DirFS(root)
	fileServer := http.FileServer(http.FS(fsys))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if token != "" && !tokenMatches(r, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="news"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// 消毒：以 "/" 为起点 Clean，".." 会被解析掉且无法逃出根目录。
		rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if hasHiddenSegment(rel) {
			http.NotFound(w, r)
			return
		}
		probe := rel
		if probe == "" {
			probe = "."
		}
		target, err := fs.Stat(fsys, probe)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		servePath := "/" + rel
		if target.IsDir() {
			// 只接受目录下的 index.html；其余情况一律 404，永不列目录。
			if _, err := fs.Stat(fsys, path.Join(probe, "index.html")); err != nil {
				http.NotFound(w, r)
				return
			}
			// 交给 FileServer 走它自己的 index.html 处理（它会补上末尾斜杠），
			// 不要把路径改写成 "/index.html"——那会触发到 "./" 的 301 跳转。
			if !strings.HasSuffix(servePath, "/") {
				servePath += "/"
			}
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; style-src 'self'; img-src 'self' data:; "+
				"base-uri 'none'; form-action 'none'; frame-ancestors 'none'")

		clone := r.Clone(r.Context())
		clone.URL.Path = servePath
		fileServer.ServeHTTP(w, clone)
	})
	return handler, root, nil
}

// hasHiddenSegment reports whether any path segment starts with ".". Such files
// (.env, .git, .DS_Store) must never be served even if they end up under
// PublicDir.
func hasHiddenSegment(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// tokenMatches accepts the token as a Bearer header or as ?t= so the page can
// be opened directly from a chat message.
func tokenMatches(r *http.Request, want string) bool {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if strings.TrimPrefix(h, "Bearer ") == want {
			return true
		}
	}
	if r.URL.Query().Get("t") == want {
		return true
	}
	return false
}

// gzipWriter compresses text responses. It is skipped for HEAD and Range
// requests, where changing the body encoding would break semantics.
type gzipWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	compress    bool
	wroteHeader bool
}

func (g *gzipWriter) WriteHeader(code int) {
	ct := g.Header().Get("Content-Type")
	g.compress = code == http.StatusOK && strings.HasPrefix(ct, "text/")
	if g.compress {
		g.Header().Del("Content-Length")
		g.Header().Set("Content-Encoding", "gzip")
		g.Header().Add("Vary", "Accept-Encoding")
	}
	g.wroteHeader = true
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	// handler 可能从不显式调用 WriteHeader（直接 Write 会隐式补 200），
	// 这时必须在这里补做决定，否则响应会被原样透传、压缩被静默跳过。
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.compress {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next.ServeHTTP(&gzipWriter{ResponseWriter: w, gz: gz}, r)
	})
}
