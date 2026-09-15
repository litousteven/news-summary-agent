#!/usr/bin/env python3
"""RSS 源体检：复刻 pipeline/fetch_rss 的抓取参数逐个实测。

用法: python3 audit_feeds.py [feeds.yaml 路径]
"""
import sys
import re
import time
import urllib.request
import urllib.error
import xml.etree.ElementTree as ET
from datetime import datetime, timezone

PROXY = "http://127.0.0.1:7890"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
ACCEPT = "application/rss+xml, application/atom+xml, application/xml, text/xml, */*"
NOW = datetime.now(timezone.utc)

DATE_FORMATS = [
    "%a, %d %b %Y %H:%M:%S %z",
    "%a, %d %b %Y %H:%M:%S %Z",
    "%Y-%m-%dT%H:%M:%S%z",
    "%Y-%m-%dT%H:%M:%SZ",
    "%Y-%m-%d %H:%M:%S",
]


def parse_feeds(path):
    """解析 feeds.yaml 的字段（格式固定，避免引入 PyYAML 依赖）。"""
    feeds, cur = [], None
    for line in open(path, encoding="utf-8"):
        line = line.rstrip("\n")
        if re.match(r"^\s*#", line) or not line.strip():
            continue
        m = re.match(r"^-\s+name:\s*(.+?)\s*$", line)
        if m:
            if cur:
                feeds.append(cur)
            cur = {"name": m.group(1), "enabled": False, "use_proxy": False}
            continue
        if cur is None:
            continue
        for key in ("url", "lang"):
            m = re.match(r"^\s+%s:\s*(.+?)\s*$" % key, line)
            if m:
                cur[key] = m.group(1)
        m = re.match(r"^\s+use_proxy:\s*(\S+)", line)
        if m:
            cur["use_proxy"] = m.group(1).lower() == "true"
        m = re.match(r"^\s+enabled:\s*(\S+)", line)
        if m:
            cur["enabled"] = m.group(1).lower() == "true"
    if cur:
        feeds.append(cur)
    return feeds


def parse_time(s):
    s = (s or "").strip()
    if not s:
        return None
    for fmt in DATE_FORMATS:
        try:
            t = datetime.strptime(s, fmt)
            if t.tzinfo is None:
                t = t.replace(tzinfo=timezone.utc)
            return t
        except ValueError:
            continue
    return None


def first_tag(el, names):
    """在命名空间不确定的情况下找第一个匹配的标签文本。"""
    for child in el.iter():
        tag = child.tag.split("}")[-1].lower()
        if tag in names and (child.text or "").strip():
            return child.text.strip()
    return ""


def audit(feed):
    url = feed.get("url", "")
    result = {"name": feed["name"], "url": url, "items": 0, "latest": None,
              "age": None, "no_date": 0, "err": "", "ms": 0}
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({"http": PROXY, "https": PROXY} if feed["use_proxy"] else {})
    )
    req = urllib.request.Request(url, headers={"User-Agent": UA, "Accept": ACCEPT})
    start = time.time()
    try:
        with opener.open(req, timeout=20) as resp:
            body = resp.read()
            result["http"] = resp.status
    except urllib.error.HTTPError as e:
        result["ms"] = int((time.time() - start) * 1000)
        result["err"] = "HTTP %d" % e.code
        return result
    except Exception as e:
        result["ms"] = int((time.time() - start) * 1000)
        result["err"] = type(e).__name__ + ": " + str(e)[:70]
        return result
    result["ms"] = int((time.time() - start) * 1000)
    result["bytes"] = len(body)

    try:
        root = ET.fromstring(body)
    except Exception as e:
        result["err"] = "XML 解析失败: " + str(e)[:50]
        return result

    items = [el for el in root.iter() if el.tag.split("}")[-1].lower() in ("item", "entry")]
    result["items"] = len(items)
    latest = None
    for it in items[:20]:
        raw = first_tag(it, ("pubdate", "published", "updated", "date"))
        t = parse_time(raw)
        if t is None:
            result["no_date"] += 1
            continue
        if latest is None or t > latest:
            latest = t
    result["latest"] = latest
    if latest:
        result["age"] = (NOW - latest).total_seconds() / 86400
    return result


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else "config/feeds.yaml"
    feeds = parse_feeds(path)
    enabled = [f for f in feeds if f["enabled"]]
    disabled = [f for f in feeds if not f["enabled"]]
    print("共 %d 个源，启用 %d 个，禁用 %d 个\n" % (len(feeds), len(enabled), len(disabled)))

    print("=" * 108)
    print("启用中的源")
    print("=" * 108)
    print("%-16s %-6s %6s %5s %6s %9s  %s" % ("源", "代理", "HTTP", "条数", "无日期", "最新(天前)", "耗时/错误"))
    print("-" * 108)
    bad = []
    for f in enabled:
        r = audit(f)
        age = "%.2f" % r["age"] if r["age"] is not None else "  -"
        flag = ""
        if r["err"]:
            flag = "❌ " + r["err"]
            bad.append((f["name"], r["err"]))
        elif r["age"] is not None and r["age"] > 3:
            flag = "⚠ 可能停更"
            bad.append((f["name"], "最新条目 %.1f 天前" % r["age"]))
        elif r["items"] == 0:
            flag = "⚠ 无条目"
            bad.append((f["name"], "解析出 0 条"))
        print("%-16s %-6s %6s %5d %6d %9s  %s%s" % (
            r["name"], "是" if f["use_proxy"] else "否",
            r.get("http", "-"), r["items"], r["no_date"], age,
            "%dms  " % r["ms"] if not r["err"] else "", flag))

    print()
    print("=" * 108)
    print("问题源汇总（%d 个）" % len(bad))
    print("=" * 108)
    for name, why in bad:
        print("  ❌ %-16s %s" % (name, why))
    if not bad:
        print("  无")

    print()
    print("=" * 108)
    print("已禁用的源（对照，看是否值得重新启用）")
    print("=" * 108)
    for f in disabled:
        r = audit(f)
        age = "%.2f" % r["age"] if r["age"] is not None else "  -"
        status = r["err"] if r["err"] else "HTTP %s / %d 条 / 最新 %s 天前" % (
            r.get("http"), r["items"], age)
        print("  %-18s %s" % (f["name"], status))


if __name__ == "__main__":
    main()
