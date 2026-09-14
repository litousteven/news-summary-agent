package site

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Options configures one site generation.
type Options struct {
	DataDir   string // where digest_*.md live
	PublicDir string // where the static site is written
	BaseURL   string // optional absolute base, e.g. "https://news.example.com/"
	FeedLimit int    // how many recent digests go into feed.xml
}

type pageData struct {
	Digest   Digest
	Archive  []ArchiveEntry
	Current  string
	IsLatest bool
}

// Generate renders the whole static site from the digests currently on disk.
//
// Digest pages are written per slug and never deleted, so the archive outlives
// data/ — the pipeline expires digest_*.md after file_expiry_days, and the
// generated HTML is what preserves history for the site.
func Generate(opts Options) error {
	if opts.FeedLimit <= 0 {
		opts.FeedLimit = 20
	}
	base := normalizeBaseURL(opts.BaseURL)

	digests, errs := LoadDigests(opts.DataDir)
	for _, err := range errs {
		log.Printf("[Site] %v", err)
	}
	if len(digests) == 0 {
		return fmt.Errorf("在 %s 没找到可解析的 digest_*.md", opts.DataDir)
	}

	pagesDir := filepath.Join(opts.PublicDir, "d")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录: %w", err)
	}

	tmpl, err := template.New("page").Parse(pageHTML)
	if err != nil {
		return fmt.Errorf("解析模板: %w", err)
	}
	if _, err := tmpl.New("article").Parse(articleHTML); err != nil {
		return fmt.Errorf("解析模板: %w", err)
	}

	archive, err := ArchiveFromPages(opts.PublicDir)
	if err != nil {
		return fmt.Errorf("读取归档: %w", err)
	}

	for _, d := range digests {
		data := pageData{Digest: d, Archive: archive, Current: d.Slug}
		if err := writeTemplate(filepath.Join(pagesDir, d.Slug+".html"), tmpl, data); err != nil {
			return err
		}
	}

	// index.html 展示最新一期。归档列表在写完本期页面后再取一次，
	// 保证首次生成时最新一期也出现在列表里。
	archive, err = ArchiveFromPages(opts.PublicDir)
	if err != nil {
		return fmt.Errorf("读取归档: %w", err)
	}
	latest := digests[0]
	if err := writeTemplate(filepath.Join(opts.PublicDir, "index.html"), tmpl, pageData{
		Digest: latest, Archive: archive, Current: latest.Slug, IsLatest: true,
	}); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(opts.PublicDir, "style.css"), []byte(styleCSS), 0o644); err != nil {
		return fmt.Errorf("写入 style.css: %w", err)
	}
	if err := writeFeed(opts.PublicDir, base, tmpl, digests, opts.FeedLimit); err != nil {
		return err
	}

	log.Printf("[Site] 已生成: %d 期 → %s（最新 %s，归档 %d 期）",
		len(digests), opts.PublicDir, latest.Date, len(archive))
	return nil
}

func writeTemplate(path string, tmpl *template.Template, data pageData) error {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page", data); err != nil {
		return fmt.Errorf("渲染 %s: %w", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", path, err)
	}
	return nil
}

// writeFeed emits an Atom feed whose entries hold the fully rendered article
// HTML, so the digest reads the same in a reader as on the page.
func writeFeed(publicDir, base string, tmpl *template.Template, digests []Digest, limit int) error {
	if limit > len(digests) {
		limit = len(digests)
	}
	self := base
	var entries strings.Builder
	updated := time.Now().Format(time.RFC3339)
	for _, d := range digests[:limit] {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "article", pageData{Digest: d, Current: d.Slug, IsLatest: true}); err != nil {
			return fmt.Errorf("渲染 feed 条目 %s: %w", d.Slug, err)
		}
		link := fmt.Sprintf("%sd/%s.html", self, d.Slug)
		when := d.When
		if when.IsZero() {
			when = time.Now()
		}
		entries.WriteString("<entry>\n")
		entries.WriteString(fmt.Sprintf("<title>%s</title>\n", html.EscapeString("新闻简报 "+d.Date)))
		entries.WriteString(fmt.Sprintf("<link href=\"%s\"/>\n", html.EscapeString(link)))
		entries.WriteString(fmt.Sprintf("<id>%s</id>\n", html.EscapeString(link)))
		entries.WriteString(fmt.Sprintf("<updated>%s</updated>\n", when.Format(time.RFC3339)))
		entries.WriteString(fmt.Sprintf("<content type=\"html\">%s</content>\n", html.EscapeString(buf.String())))
		entries.WriteString("</entry>\n")
	}
	body := fmt.Sprintf(feedXMLTemplate,
		html.EscapeString(base),
		html.EscapeString(self),
		updated,
		html.EscapeString(base),
		entries.String(),
	)
	if err := os.WriteFile(filepath.Join(publicDir, "feed.xml"), []byte(body), 0o644); err != nil {
		return fmt.Errorf("写入 feed.xml: %w", err)
	}
	return nil
}

// normalizeBaseURL makes the base usable for concatenation and rejects values
// that cannot be a URL prefix, rather than emitting broken absolute links.
func normalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	if !strings.Contains(raw, "://") {
		log.Printf("[Site] 忽略不合法的 base-url=%q（需要 http(s):// 前缀）", raw)
		return "/"
	}
	if !strings.HasSuffix(raw, "/") {
		raw += "/"
	}
	return raw
}
