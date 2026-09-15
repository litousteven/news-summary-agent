package fetch_rss

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const tinyFeed = `<?xml version="1.0"?>
<rss version="2.0"><channel><title>t</title>
<item><title>新闻一</title><link>https://example.com/1</link>
<description>摘要一</description><pubDate>Tue, 15 Sep 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`

// flakyServer 前 failTimes 次返回 500，之后返回正常 feed。
func flakyServer(failTimes int, calls *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(calls, 1)
		if int(n) <= failTimes {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(tinyFeed))
	}))
}

func testSource(url string) FeedSource {
	return FeedSource{Name: "测试源", URL: url, Lang: "zh", Enabled: true}
}

// 核心：代理抖动一次不应丢掉整个源。
func TestFetchFeedWithRetry_RecoversFromTransientFailure(t *testing.T) {
	var calls int32
	srv := flakyServer(2, &calls) // 前两次 500，第三次成功
	defer srv.Close()

	items, err := FetchFeedWithRetry(context.Background(), testSource(srv.URL), "",
		2, time.Millisecond)
	if err != nil {
		t.Fatalf("应重试后成功: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应取到 1 条，实际 %d", len(items))
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("调用次数 = %d，期望 3（2 次失败 + 1 次成功）", got)
	}
}

// 一直失败时，尝试次数应为 maxRetries+1 并返回错误。
func TestFetchFeedWithRetry_ExhaustsAttempts(t *testing.T) {
	var calls int32
	srv := flakyServer(1<<30, &calls) // 永远 500
	defer srv.Close()

	_, err := FetchFeedWithRetry(context.Background(), testSource(srv.URL), "",
		2, time.Millisecond)
	if err == nil {
		t.Fatal("一直失败应返回错误")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("调用次数 = %d，期望 3", got)
	}
}

// 4xx 是源本身的问题，重试没有意义——必须只请求一次。
func TestFetchFeedWithRetry_DoesNotRetryPermanent4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchFeedWithRetry(context.Background(), testSource(srv.URL), "",
		3, time.Millisecond)
	if err == nil {
		t.Fatal("404 应返回错误")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("4xx 不应重试，实际请求 %d 次", got)
	}
}

// maxRetries=0 时退化为一次尝试，保持旧行为可用。
func TestFetchFeedWithRetry_ZeroRetries(t *testing.T) {
	var calls int32
	srv := flakyServer(1, &calls)
	defer srv.Close()

	_, err := FetchFeedWithRetry(context.Background(), testSource(srv.URL), "",
		0, time.Millisecond)
	if err == nil {
		t.Fatal("不重试时一次失败即应返回错误")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("调用次数 = %d，期望 1", got)
	}
}

// 重试等待期间取消 context 应立即返回，不把退避时间睡满。
func TestFetchFeedWithRetry_RespectsContextCancel(t *testing.T) {
	var calls int32
	srv := flakyServer(1<<30, &calls)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, err := FetchFeedWithRetry(ctx, testSource(srv.URL), "", 5, 2*time.Second); err == nil {
		t.Fatal("取消后应返回错误")
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("取消后应立即返回，实际耗时 %v", elapsed)
	}
}

func TestIsPermanentFeedError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("fetch X: status 404"), true},
		{fmt.Errorf("fetch X: status 403"), true},
		{fmt.Errorf("fetch X: status 500"), false}, // 5xx 可重试
		{fmt.Errorf("fetch X: read tcp: connection reset by peer"), false},
		{fmt.Errorf("fetch X: context deadline exceeded"), false},
	}
	for _, c := range cases {
		if got := isPermanentFeedError(c.err); got != c.want {
			t.Errorf("isPermanentFeedError(%v) = %v，期望 %v", c.err, got, c.want)
		}
	}
}
