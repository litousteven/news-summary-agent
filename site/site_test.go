package site

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 用一份与生产完全同形的 digest 作为夹具（含 Ref、统计行、来源链接）。
const sampleDigest = `# 新闻简报 2026-09-14 18:24

## 能源与资源控制

- 9月12日，胡塞武装攻占红海战略要地，沙特被迫关闭关键石油管道。
  [NPR](https://www.npr.org/2026/09/12/nx-s1-5964940/red-sea)
  > 相关: [胡塞武装占领战略要地](https://www.chinanews.com.cn/gj/2026/09-11/10695019.shtml)（中新网）
- 9月14日，国际油价已突破每桶108美元。
  [Financial Times](https://www.ft.com/content/4845a503)

## 军事战略与部署

- 9月14日，芬兰决定加入法国提出的“前沿威慑”核计划。
  [中新网](https://www.chinanews.com.cn/gj/2026/09-14/10696257.shtml)

---
统计: 抓取=120, 标注=115, 入选=10
`

func TestParseDigest_RealShape(t *testing.T) {
	d, err := ParseDigest(sampleDigest)
	if err != nil {
		t.Fatalf("ParseDigest 返回错误: %v", err)
	}
	if d.Date != "2026-09-14 18:24" {
		t.Errorf("Date = %q", d.Date)
	}
	if !d.When.Equal(mustTime(t, "2026-09-14 18:24")) {
		t.Errorf("When = %v", d.When)
	}
	if d.Stats != "抓取=120, 标注=115, 入选=10" {
		t.Errorf("Stats = %q", d.Stats)
	}
	if len(d.Sections) != 2 {
		t.Fatalf("Sections = %d，期望 2", len(d.Sections))
	}
	if d.Items != 3 {
		t.Errorf("Items = %d，期望 3", d.Items)
	}

	first := d.Sections[0].Items[0]
	if first.Source != "NPR" || first.Link != "https://www.npr.org/2026/09/12/nx-s1-5964940/red-sea" {
		t.Errorf("来源解析错误: source=%q link=%q", first.Source, first.Link)
	}
	if len(first.Refs) != 1 {
		t.Fatalf("Refs = %d，期望 1", len(first.Refs))
	}
	ref := first.Refs[0]
	if ref.Note != "相关" || ref.Source != "中新网" ||
		ref.Title != "胡塞武装占领战略要地" ||
		ref.Link != "https://www.chinanews.com.cn/gj/2026/09-11/10695019.shtml" {
		t.Errorf("Ref 解析错误: %+v", ref)
	}

	// 第二条没有 Ref，不能把上一条的 Ref 带过去
	if len(d.Sections[0].Items[1].Refs) != 0 {
		t.Errorf("第二条不应有 Refs: %+v", d.Sections[0].Items[1].Refs)
	}
}

func TestParseDigest_RejectsNonDigest(t *testing.T) {
	if _, err := ParseDigest("这不是简报\n只有一些文字\n"); err == nil {
		t.Error("缺少标题行时应返回错误")
	}
}

func TestSlugLabel(t *testing.T) {
	if got, want := SlugLabel("20260914_182457"), "2026-09-14 18:24"; got != want {
		t.Errorf("SlugLabel = %q，期望 %q", got, want)
	}
	// 无法解析的 slug 原样返回，不要显示成零值时间
	if got := SlugLabel("weird-slug"); got != "weird-slug" {
		t.Errorf("无法解析时应原样返回，got %q", got)
	}
}

// 生成的页面必须转义内容：新闻正文与 LLM 输出都可能含尖括号。
func TestGenerate_EscapesAndArchives(t *testing.T) {
	dataDir, publicDir := t.TempDir(), t.TempDir()
	body := strings.Replace(sampleDigest, "芬兰决定加入", "<script>alert(1)</script>芬兰决定加入", 1)
	writeFile(t, filepath.Join(dataDir, "digest_20260914_182457.md"), body)

	if err := Generate(Options{DataDir: dataDir, PublicDir: publicDir, BaseURL: "https://news.example.com/"}); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}

	index := readFile(t, filepath.Join(publicDir, "index.html"))
	if strings.Contains(index, "<script>alert(1)</script>") {
		t.Error("生成页面未转义注入内容")
	}
	if !strings.Contains(index, "&lt;script&gt;") {
		t.Error("转义后的文本应出现在页面中")
	}
	for _, want := range []string{"能源与资源控制", "胡塞武装", "https://www.npr.org", "/d/20260914_182457.html", "/feed.xml"} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html 缺少 %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(publicDir, "d", "20260914_182457.html")); err != nil {
		t.Errorf("缺少单期页面: %v", err)
	}
	if _, err := os.Stat(filepath.Join(publicDir, "style.css")); err != nil {
		t.Errorf("缺少 style.css: %v", err)
	}
	feed := readFile(t, filepath.Join(publicDir, "feed.xml"))
	if !strings.Contains(feed, "<feed xmlns=\"http://www.w3.org/2005/Atom\">") ||
		!strings.Contains(feed, "https://news.example.com/d/20260914_182457.html") {
		t.Errorf("feed.xml 内容不符合预期:\n%s", feed[:min(400, len(feed))])
	}
}

// 归档列表来自已生成的页面，因此 data/ 被清理后历史仍然保留。
func TestGenerate_ArchiveSurvivesDataCleanup(t *testing.T) {
	dataDir, publicDir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dataDir, "digest_20260914_182457.md"), sampleDigest)
	if err := Generate(Options{DataDir: dataDir, PublicDir: publicDir}); err != nil {
		t.Fatalf("首次生成失败: %v", err)
	}

	// 模拟 file_expiry_days 清理：data/ 只剩新一期
	if err := os.Remove(filepath.Join(dataDir, "digest_20260914_182457.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dataDir, "digest_20260915_060000.md"),
		strings.Replace(sampleDigest, "2026-09-14 18:24", "2026-09-15 06:00", 1))
	if err := Generate(Options{DataDir: dataDir, PublicDir: publicDir}); err != nil {
		t.Fatalf("二次生成失败: %v", err)
	}

	index := readFile(t, filepath.Join(publicDir, "index.html"))
	if !strings.Contains(index, "/d/20260914_182457.html") {
		t.Error("旧一期应从归档列表保留，不能因 data/ 清理而消失")
	}
	if _, err := os.Stat(filepath.Join(publicDir, "d", "20260914_182457.html")); err != nil {
		t.Errorf("旧一期页面不应被删除: %v", err)
	}
}

func TestGenerate_ErrorsWhenNoDigests(t *testing.T) {
	if err := Generate(Options{DataDir: t.TempDir(), PublicDir: t.TempDir()}); err == nil {
		t.Error("没有 digest 时应返回错误")
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                          "/",
		"https://news.example.com":  "https://news.example.com/",
		"https://news.example.com/": "https://news.example.com/",
		"http://a.b/c":              "http://a.b/c/",
		"news.example.com":          "/", // 无 scheme，拒绝
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// —— 服务端安全边界 ——

func newTestServer(t *testing.T, token string) (http.Handler, string) {
	t.Helper()
	publicDir := t.TempDir()
	writeFile(t, filepath.Join(publicDir, "index.html"), "<html>ok</html>")
	writeFile(t, filepath.Join(publicDir, "style.css"), "body{}")
	if err := os.MkdirAll(filepath.Join(publicDir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(publicDir, "d", "20260914_182457.html"), "<html>one</html>")
	// 敏感文件：即便出现在 public 目录下也不能被读走
	writeFile(t, filepath.Join(publicDir, ".env"), "SECRET=1")

	handler, _, err := newStaticHandler(publicDir, token)
	if err != nil {
		t.Fatalf("newStaticHandler: %v", err)
	}
	return handler, publicDir
}

func TestServe_SecurityBoundaries(t *testing.T) {
	handler, publicDir := newTestServer(t, "")

	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"首页", http.MethodGet, "/", http.StatusOK},
		{"单期页面", http.MethodGet, "/d/20260914_182457.html", http.StatusOK},
		{"样式", http.MethodGet, "/style.css", http.StatusOK},
		{"健康检查", http.MethodGet, "/healthz", http.StatusOK},
		{"不存在", http.MethodGet, "/nope.html", http.StatusNotFound},
		{"禁止列目录", http.MethodGet, "/d/", http.StatusNotFound},
		{"禁止目录跳转", http.MethodGet, "/d", http.StatusNotFound},
		{"隐藏文件", http.MethodGet, "/.env", http.StatusNotFound},
		{"路径穿越", http.MethodGet, "/../../../../etc/passwd", http.StatusNotFound},
		{"编码穿越", http.MethodGet, "/%2e%2e/%2e%2e/etc/passwd", http.StatusNotFound},
		{"POST 拒绝", http.MethodPost, "/", http.StatusMethodNotAllowed},
		{"DELETE 拒绝", http.MethodDelete, "/", http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("%s %s = %d，期望 %d", c.method, c.path, rec.Code, c.want)
			}
		})
	}

	// 确认 public 目录之外的内容确实取不到
	if _, err := os.Stat(filepath.Join(publicDir, ".env")); err != nil {
		t.Skipf("夹具缺失: %v", err)
	}
}

func TestServe_SecurityHeaders(t *testing.T) {
	handler, _ := newTestServer(t, "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for _, h := range []string{"X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options", "Content-Security-Policy", "Cache-Control"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("缺少安全响应头 %s", h)
		}
	}
}

func TestServe_Token(t *testing.T) {
	handler, _ := newTestServer(t, "s3cret")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("无口令应 401，实际 %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?t=s3cret", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("正确口令应 200，实际 %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Bearer 口令应 200，实际 %d", rec.Code)
	}

	// 健康检查不要求口令，便于外部探活
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz 应免鉴权，实际 %d", rec.Code)
	}
}

func TestWithGzip_CompressesTextOnly(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Repeat("新闻", 50)))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	withGzip(next).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q，期望 gzip", got)
	}
	if rec.Body.Len() == 0 {
		t.Error("压缩后响应体不应为空")
	}

	// HEAD 不压缩
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodHead, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	withGzip(next).ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Encoding"); got == "gzip" {
		t.Error("HEAD 请求不应启用 gzip")
	}
}

// —— 测试辅助 ——

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入 %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return string(b)
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	return parseDigestDate(s)
}
