// Package site turns the pipeline's digest Markdown into a static news site and
// serves it over HTTP.
//
// The digest files are the only input, so the parser below is deliberately
// written against the exact shape writeDigestMD emits. Anything it does not
// recognize is ignored rather than guessed at.
package site

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Digest is one parsed digest file.
type Digest struct {
	Slug     string // filename stem without the "digest_" prefix, e.g. "20260914_182457"
	Date     string // display form, e.g. "2026-09-14 18:24"
	When     time.Time
	Sections []Section
	Stats    string
	Items    int
}

// Section is one "## 分类" block.
type Section struct {
	Category string
	Items    []Item
}

// Item is one "- ..." news entry with its source link and related references.
type Item struct {
	Text   string
	Source string
	Link   string
	Refs   []Ref
}

// Ref is a "> 相关: [标题](链接)（来源）" line.
type Ref struct {
	Note   string
	Title  string
	Link   string
	Source string
}

var (
	headerRe   = regexp.MustCompile(`^#\s+新闻简报\s+(.+?)\s*$`)
	categoryRe = regexp.MustCompile(`^##\s+(.+?)\s*$`)
	itemRe     = regexp.MustCompile(`^-\s+(.*)$`)
	sourceRe   = regexp.MustCompile(`^\s*\[([^\]]+)\]\((\S+?)\)\s*$`)
	plainSrcRe = regexp.MustCompile(`^\s*来源[:：]\s*(.+?)\s*$`)
	refRe      = regexp.MustCompile(`^\s*>\s*(.*)$`)
	statsRe    = regexp.MustCompile(`^统计[:：]\s*(.+?)\s*$`)
	// 引用行内部：[标题](链接) 与结尾的（来源）
	linkRe = regexp.MustCompile(`\[([^\]]+)\]\((\S+?)\)`)
	tailRe = regexp.MustCompile(`[（(]([^（()）]+)[）)]\s*$`)
)

// LoadDigests parses every digest_*.md in dataDir, newest first. A file that
// fails to parse is skipped with an error rather than aborting the whole site:
// one bad digest must not blank the page.
func LoadDigests(dataDir string) ([]Digest, []error) {
	paths, err := filepath.Glob(filepath.Join(dataDir, "digest_*.md"))
	if err != nil {
		return nil, []error{err}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))

	var digests []Digest
	var errs []error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("读取 %s: %w", path, err))
			continue
		}
		d, err := ParseDigest(string(data))
		if err != nil {
			errs = append(errs, fmt.Errorf("解析 %s: %w", path, err))
			continue
		}
		d.Slug = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "digest_"), ".md")
		digests = append(digests, d)
	}
	return digests, errs
}

// ParseDigest parses one digest document. It returns an error only when the
// document has no recognizable header, which means it is not a digest at all.
func ParseDigest(body string) (Digest, error) {
	var d Digest
	var curSection *Section
	var curItem *Item
	inFooter := false

	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}
		if trimmed == "---" {
			inFooter = true
			curItem = nil
			continue
		}
		if m := statsRe.FindStringSubmatch(trimmed); m != nil {
			d.Stats = m[1]
			continue
		}
		if inFooter {
			continue
		}
		if m := headerRe.FindStringSubmatch(trimmed); m != nil {
			d.Date = m[1]
			d.When = parseDigestDate(m[1])
			continue
		}
		if m := categoryRe.FindStringSubmatch(trimmed); m != nil {
			d.Sections = append(d.Sections, Section{Category: m[1]})
			curSection = &d.Sections[len(d.Sections)-1]
			curItem = nil
			continue
		}
		if m := itemRe.FindStringSubmatch(line); m != nil {
			if curSection == nil {
				// 没有分类标题的条目：归入兜底分类，避免内容丢失
				d.Sections = append(d.Sections, Section{Category: "其他"})
				curSection = &d.Sections[len(d.Sections)-1]
			}
			curSection.Items = append(curSection.Items, Item{Text: m[1]})
			curItem = &curSection.Items[len(curSection.Items)-1]
			d.Items++
			continue
		}
		if curItem == nil {
			continue
		}
		if m := sourceRe.FindStringSubmatch(line); m != nil {
			curItem.Source, curItem.Link = m[1], m[2]
			continue
		}
		if m := plainSrcRe.FindStringSubmatch(line); m != nil {
			curItem.Source = m[1]
			continue
		}
		if m := refRe.FindStringSubmatch(line); m != nil {
			curItem.Refs = append(curItem.Refs, parseRef(m[1]))
			continue
		}
	}

	if err := sc.Err(); err != nil {
		return d, err
	}
	if d.Date == "" {
		return d, fmt.Errorf("缺少「# 新闻简报 <时间>」标题行")
	}
	return d, nil
}

// parseRef splits "相关: [标题](链接)（来源）" into its parts. A ref without a
// link still keeps its label so the relationship is visible.
func parseRef(body string) Ref {
	ref := Ref{Note: "相关"}
	rest := strings.TrimSpace(body)
	if i := strings.Index(rest, ":"); i > 0 {
		ref.Note = strings.TrimSpace(rest[:i])
		rest = strings.TrimSpace(rest[i+1:])
	}
	if m := linkRe.FindStringSubmatch(rest); m != nil {
		ref.Title, ref.Link = m[1], m[2]
		rest = strings.Replace(rest, m[0], "", 1)
	}
	if m := tailRe.FindStringSubmatch(rest); m != nil {
		ref.Source = strings.TrimSpace(m[1])
		rest = strings.TrimSpace(rest[:len(rest)-len(m[0])])
	}
	if ref.Title == "" {
		ref.Title = strings.TrimSpace(rest)
	}
	return ref
}

var digestDateLayouts = []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02"}

func parseDigestDate(s string) time.Time {
	for _, layout := range digestDateLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ArchiveEntry is one row in the site's archive list. It is derived from the
// generated pages rather than from data/, so the list survives the pipeline's
// file-expiry cleanup.
type ArchiveEntry struct {
	Slug  string
	Label string
}

// ArchiveFromPages lists generated digest pages, newest first.
func ArchiveFromPages(publicDir string) ([]ArchiveEntry, error) {
	paths, err := filepath.Glob(filepath.Join(publicDir, "d", "*.html"))
	if err != nil {
		return nil, err
	}
	entries := make([]ArchiveEntry, 0, len(paths))
	for _, path := range paths {
		slug := strings.TrimSuffix(filepath.Base(path), ".html")
		entries = append(entries, ArchiveEntry{Slug: slug, Label: SlugLabel(slug)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Slug > entries[j].Slug })
	return entries, nil
}

// SlugLabel renders "20260914_182457" as "2026-09-14 18:24".
func SlugLabel(slug string) string {
	t, err := time.ParseInLocation("20060102_150405", slug, time.Local)
	if err != nil {
		return slug
	}
	return t.Format("2006-01-02 15:04")
}
