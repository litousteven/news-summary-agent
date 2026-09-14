package site

// pageHTML is the whole-site template. "article" is also executed on its own
// when building the Atom feed.
const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{if .IsLatest}}新闻简报{{else}}{{.Digest.Date}} · 新闻简报{{end}}</title>
<link rel="stylesheet" href="/style.css">
<link rel="alternate" type="application/atom+xml" title="新闻简报" href="/feed.xml">
</head>
<body>
<header class="top">
<a class="brand" href="/">新闻简报</a>
<span class="stamp">{{.Digest.Date}}</span>
</header>
<main>
{{template "article" .}}
</main>
<nav class="archive">
<h2>往期</h2>
<ul>
{{range .Archive}}<li{{if eq .Slug $.Current}} class="on"{{end}}><a href="/d/{{.Slug}}.html">{{.Label}}</a></li>
{{end}}</ul>
</nav>
<footer><p>由 news-summary-agent 自动生成 · <a href="/feed.xml">RSS 订阅</a></p></footer>
</body>
</html>
`

// articleHTML renders one digest. Everything is escaped by html/template, so
// feed text and LLM output cannot inject markup.
const articleHTML = `{{define "article"}}<article>
{{range .Digest.Sections}}<section class="cat">
<h2>{{.Category}}</h2>
<ul class="items">
{{range .Items}}<li>
<p class="text">{{.Text}}</p>
{{if .Link}}<a class="src" href="{{.Link}}" target="_blank" rel="noopener noreferrer">{{.Source}}</a>{{else}}<span class="src">{{.Source}}</span>{{end}}
{{if .Refs}}<ul class="refs">
{{range .Refs}}<li><span class="note">{{.Note}}</span>{{if .Link}} <a href="{{.Link}}" target="_blank" rel="noopener noreferrer">{{.Title}}</a>{{else}} {{.Title}}{{end}}{{if .Source}} <span class="from">{{.Source}}</span>{{end}}</li>
{{end}}</ul>{{end}}
</li>
{{end}}</ul>
</section>
{{end}}
{{if .Digest.Stats}}<p class="stats">{{.Digest.Stats}}</p>{{end}}
</article>{{end}}
`

// styleCSS is mobile-first: the page is mostly read on a phone after the QQ push.
const styleCSS = `:root{
--bg:#f7f7f5;--fg:#1c1c1e;--dim:#6b6b70;--card:#fff;--line:#e3e3e0;--accent:#0a6cff;--tag:#f0f0ee;
}
@media (prefers-color-scheme:dark){:root{
--bg:#131315;--fg:#e8e8ea;--dim:#9a9aa1;--card:#1c1c1f;--line:#2c2c30;--accent:#5aa2ff;--tag:#25252a;
}}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{
margin:0;background:var(--bg);color:var(--fg);
font:16px/1.75 -apple-system,BlinkMacSystemFont,"PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif;
}
a{color:var(--accent);text-decoration:none}
a:hover{text-decoration:underline}
.top{
display:flex;align-items:baseline;gap:.6em;flex-wrap:wrap;
padding:18px 20px;border-bottom:1px solid var(--line);
position:sticky;top:0;background:var(--bg);z-index:2;
}
.brand{font-size:19px;font-weight:700;color:var(--fg)}
.stamp{font-size:13px;color:var(--dim);font-variant-numeric:tabular-nums}
main{max-width:760px;margin:0 auto;padding:20px}
.cat{margin:0 0 26px}
.cat>h2{
font-size:14px;font-weight:600;color:var(--dim);letter-spacing:.06em;
margin:0 0 10px;padding-bottom:6px;border-bottom:1px solid var(--line);
}
.items{list-style:none;margin:0;padding:0}
.items>li{
background:var(--card);border:1px solid var(--line);border-radius:10px;
padding:13px 15px;margin-bottom:10px;
}
.text{margin:0;word-break:break-word}
.src{
display:inline-block;margin-top:9px;font-size:12px;color:var(--dim);
background:var(--tag);border-radius:5px;padding:2px 8px;
}
a.src:hover{color:var(--accent);text-decoration:none}
.refs{list-style:none;margin:9px 0 0;padding:9px 0 0;border-top:1px dashed var(--line);font-size:13px;color:var(--dim)}
.refs li{margin:3px 0;word-break:break-word}
.refs .note{color:var(--dim)}
.refs .from{font-size:12px;opacity:.75}
.stats{color:var(--dim);font-size:12px;text-align:center;margin:26px 0 0;font-variant-numeric:tabular-nums}
.archive{max-width:760px;margin:0 auto;padding:0 20px 40px}
.archive>h2{font-size:14px;font-weight:600;color:var(--dim);letter-spacing:.06em;margin:0 0 10px}
.archive ul{list-style:none;margin:0;padding:0;display:flex;flex-wrap:wrap;gap:8px}
.archive a{
display:inline-block;font-size:13px;padding:5px 11px;border:1px solid var(--line);
border-radius:999px;color:var(--dim);background:var(--card);font-variant-numeric:tabular-nums;
}
.archive li.on a{border-color:var(--accent);color:var(--accent)}
footer{max-width:760px;margin:0 auto;padding:0 20px 50px;color:var(--dim);font-size:12px;text-align:center}
`

// feedXMLTemplate is an Atom feed. Values are escaped with html.EscapeString
// before substitution, and the entry content is escaped HTML.
const feedXMLTemplate = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<title>新闻简报</title>
<link href="%s"/>
<link rel="self" href="%sfeed.xml"/>
<updated>%s</updated>
<id>%s</id>
%s</feed>
`
