package ddns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExtractIP(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"纯 IPv4", "123.117.214.162\n", "123.117.214.162"},
		{"纯 IPv6", "2408:8207:5410:ef41:8cb6:a48a:7a74:22a2\n", "2408:8207:5410:ef41:8cb6:a48a:7a74:22a2"},
		// ipip.net 的响应把地址夹在中文里，端口化后的解析必须还能认出来
		{"中文散文中的 IPv6", "当前 IP：2408:8207:5410:ef41:8cb6:a48a:7a74:22a2  来自于：中国 北京 北京  联通", "2408:8207:5410:ef41:8cb6:a48a:7a74:22a2"},
		{"带标签的 IPv4", "IPv4: 123.117.214.162", "123.117.214.162"},
		{"没有地址", "服务暂时不可用", ""},
		{"只有版本号不要误判", "version 1.2.3", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractIP(c.body); got != c.want {
				t.Errorf("extractIP(%q) = %q，期望 %q", c.body, got, c.want)
			}
		})
	}
}

func TestIsPublicIP(t *testing.T) {
	public := []string{"123.117.214.162", "2408:8207:5410:ef41:8cb6:a48a:7a74:22a2"}
	for _, ip := range public {
		if !isPublicIP(ip) {
			t.Errorf("%s 应被判为公网地址", ip)
		}
	}
	// 把内网/回环地址写进公网 DNS 记录会指向错误的地方，必须拒绝
	private := []string{"127.0.0.1", "10.0.0.1", "192.168.10.10", "172.16.0.1",
		"::1", "fe80::1", "fd00::1", "0.0.0.0", "不是IP"}
	for _, ip := range private {
		if isPublicIP(ip) {
			t.Errorf("%s 不应被判为公网地址", ip)
		}
	}
}

func TestConfigFromEnv_DisabledByDefault(t *testing.T) {
	t.Setenv("DDNS_ENABLED", "")
	_, enabled, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("未启用时不应报错: %v", err)
	}
	if enabled {
		t.Error("默认应处于关闭状态")
	}
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("DDNS_ENABLED", "1")
	t.Setenv("DDNS_DOMAIN", "cozyfish.site")
	t.Setenv("DDNS_API_KEY", "k")
	t.Setenv("DDNS_API_SECRET", "s")
	t.Setenv("DDNS_RECORD", "")
	t.Setenv("DDNS_RECORD_TYPE", "")
	t.Setenv("DDNS_TTL", "")
	t.Setenv("DDNS_CHECK_INTERVAL", "")

	cfg, enabled, err := ConfigFromEnv()
	if err != nil || !enabled {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
	if cfg.Record != "@" {
		t.Errorf("Record 默认应为 @，得到 %q", cfg.Record)
	}
	if cfg.RecordType != RecordTypeA {
		t.Errorf("RecordType 默认应为 A，得到 %q", cfg.RecordType)
	}
	// GoDaddy 不接受低于 600 的 TTL
	if cfg.TTL != 600 {
		t.Errorf("TTL 默认应为 600，得到 %d", cfg.TTL)
	}
	if cfg.CheckInterval != 5*time.Minute {
		t.Errorf("CheckInterval 默认应为 5m，得到 %v", cfg.CheckInterval)
	}
}

func TestConfigFromEnv_RejectsInvalid(t *testing.T) {
	base := func() {
		t.Setenv("DDNS_ENABLED", "1")
		t.Setenv("DDNS_DOMAIN", "cozyfish.site")
		t.Setenv("DDNS_API_KEY", "k")
		t.Setenv("DDNS_API_SECRET", "s")
		t.Setenv("DDNS_RECORD_TYPE", "A")
		t.Setenv("DDNS_TTL", "")
		t.Setenv("DDNS_CHECK_INTERVAL", "")
	}

	t.Run("缺少密钥", func(t *testing.T) {
		base()
		t.Setenv("DDNS_API_KEY", "")
		if _, _, err := ConfigFromEnv(); err == nil {
			t.Error("缺少 API key 应报错")
		}
	})
	t.Run("记录类型非法", func(t *testing.T) {
		base()
		t.Setenv("DDNS_RECORD_TYPE", "CNAME")
		if _, _, err := ConfigFromEnv(); err == nil {
			t.Error("CNAME 应被拒绝（本服务只维护 A/AAAA）")
		}
	})
	t.Run("TTL 非整数", func(t *testing.T) {
		base()
		t.Setenv("DDNS_TTL", "abc")
		if _, _, err := ConfigFromEnv(); err == nil {
			t.Error("非整数 TTL 应报错")
		}
	})
	t.Run("小写记录类型被接受", func(t *testing.T) {
		base()
		t.Setenv("DDNS_RECORD_TYPE", "aaaa")
		t.Setenv("DDNS_DOMAIN", "cozyfish.site")
		cfg, _, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("小写 aaaa 应被接受: %v", err)
		}
		if cfg.RecordType != RecordTypeAAAA {
			t.Errorf("RecordType = %q，期望 AAAA", cfg.RecordType)
		}
	})
}

// 探测源回退：第一个源挂掉时必须继续用第二个，而不是直接失败。
// 这是原 video-server 版本没有的能力（它只抓一个源，源坏掉后整个 DDNS 停摆）。
func TestDetectPublicIP_FallsBackAcrossProviders(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer dead.Close()
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("123.117.214.162\n"))
	}))
	defer alive.Close()

	svc := &Service{
		cfg:           Config{Domain: "d", APIKey: "k", APISecret: "s"},
		detectClient:  mustClient(5 * time.Second),
		ipv4Providers: []ipProvider{newPlainProvider("dead", dead.URL), newPlainProvider("alive", alive.URL)},
	}
	if err := svc.cfg.applyDefaults(); err != nil {
		t.Fatal(err)
	}

	ip, err := svc.detectPublicIP(context.Background())
	if err != nil {
		t.Fatalf("应回退到可用源: %v", err)
	}
	if ip != "123.117.214.162" {
		t.Errorf("ip = %q", ip)
	}
}

// A 记录绝不能接受 IPv6 地址：双栈机器上这会把域名指向错误的地址族。
func TestDetectPublicIP_RejectsWrongAddressFamily(t *testing.T) {
	onlyV6 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2408:8207:5410:ef41:8cb6:a48a:7a74:22a2\n"))
	}))
	defer onlyV6.Close()

	svc := &Service{
		cfg:           Config{Domain: "d", APIKey: "k", APISecret: "s", RecordType: RecordTypeA},
		detectClient:  mustClient(5 * time.Second),
		ipv4Providers: []ipProvider{newPlainProvider("only-v6", onlyV6.URL)},
	}
	if err := svc.cfg.applyDefaults(); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.detectPublicIP(context.Background()); err == nil {
		t.Error("A 记录遇到纯 IPv6 响应应报错")
	}
}

// 私网地址（例如探测源被劫持或返回了内网出口）不能写进公网 DNS。
func TestDetectPublicIP_RejectsPrivateAddress(t *testing.T) {
	priv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("192.168.10.10\n"))
	}))
	defer priv.Close()

	svc := &Service{
		cfg:           Config{Domain: "d", APIKey: "k", APISecret: "s"},
		detectClient:  mustClient(5 * time.Second),
		ipv4Providers: []ipProvider{newPlainProvider("private", priv.URL)},
	}
	if err := svc.cfg.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.detectPublicIP(context.Background()); err == nil {
		t.Error("私网地址应被拒绝")
	}
}

// —— 用假的 GoDaddy API 跑完整的「取记录 → 比对 → 写记录」流程 ——

type fakeGoDaddy struct {
	records   map[string]string // "type/name" → data
	putCount  int
	putBody   []byte
	authHeard string
}

func (f *fakeGoDaddy) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.authHeard = r.Header.Get("Authorization")
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/domains/"), "/")
		// parts: <domain>/records/<type>/<name>
		if len(parts) != 4 || parts[1] != "records" {
			http.NotFound(w, r)
			return
		}
		key := parts[2] + "/" + parts[3]
		switch r.Method {
		case http.MethodGet:
			data, ok := f.records[key]
			if !ok {
				_, _ = w.Write([]byte("[]"))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"data": data, "ttl": 600, "name": parts[3], "type": parts[2]}})
		case http.MethodPut:
			var body []map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) > 0 {
				f.records[key] = body[0]["data"].(string)
			}
			f.putCount++
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})
}

func newTestService(t *testing.T, cfg Config, ipBody string, gd *fakeGoDaddy) *Service {
	t.Helper()
	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ipBody))
	}))
	t.Cleanup(ipSrv.Close)
	apiSrv := httptest.NewServer(gd.handler())
	t.Cleanup(apiSrv.Close)

	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.ipv4Providers = []ipProvider{newPlainProvider("test", ipSrv.URL)}
	svc.ipv6Providers = []ipProvider{newPlainProvider("test", ipSrv.URL)}
	svc.apiBase = apiSrv.URL
	return svc
}

func TestCheckAndUpdate_UpdatesWhenChanged(t *testing.T) {
	gd := &fakeGoDaddy{records: map[string]string{"A/news": "123.117.213.182"}}
	svc := newTestService(t, Config{
		Domain: "cozyfish.site", Record: "news", RecordType: RecordTypeA,
		APIKey: "key", APISecret: "secret", TTL: 600,
	}, "123.117.214.162\n", gd)

	res, err := svc.CheckAndUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckAndUpdate: %v", err)
	}
	if !res.Updated {
		t.Error("IP 变化时应报告已更新")
	}
	if res.PublicIP != "123.117.214.162" || res.DNSIP != "123.117.213.182" {
		t.Errorf("结果不对: %+v", res)
	}
	if got := gd.records["A/news"]; got != "123.117.214.162" {
		t.Errorf("记录未更新，仍为 %q", got)
	}
	if gd.putCount != 1 {
		t.Errorf("PUT 次数 = %d，期望 1", gd.putCount)
	}
	// 凭据必须以 GoDaddy 的 sso-key 形式发送
	if !strings.HasPrefix(gd.authHeard, "sso-key key:secret") {
		t.Errorf("Authorization 头不对: %q", gd.authHeard)
	}
}

func TestCheckAndUpdate_NoOpWhenSame(t *testing.T) {
	gd := &fakeGoDaddy{records: map[string]string{"A/news": "123.117.214.162"}}
	svc := newTestService(t, Config{
		Domain: "cozyfish.site", Record: "news", RecordType: RecordTypeA,
		APIKey: "key", APISecret: "secret",
	}, "123.117.214.162\n", gd)

	res, err := svc.CheckAndUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckAndUpdate: %v", err)
	}
	if res.Updated {
		t.Error("地址相同时不应写记录")
	}
	if gd.putCount != 0 {
		t.Errorf("不应发生 PUT，实际 %d 次", gd.putCount)
	}
}

// 写入失败必须报错。原实现吞掉了 PUT 的失败结果，仍然向调用方返回「已更新」。
func TestCheckAndUpdate_PropagatesWriteFailure(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"data":"1.2.3.4"}]`))
			return
		}
		http.Error(w, `{"code":"UNABLE_TO_AUTHENTICATE"}`, http.StatusUnauthorized)
	}))
	defer apiSrv.Close()
	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("123.117.214.162\n"))
	}))
	defer ipSrv.Close()

	svc, err := New(Config{Domain: "d", Record: "news", APIKey: "k", APISecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	svc.ipv4Providers = []ipProvider{newPlainProvider("test", ipSrv.URL)}
	svc.apiBase = apiSrv.URL

	if _, err := svc.CheckAndUpdate(context.Background()); err == nil {
		t.Error("写入失败应返回错误")
	}
}

// AAAA 记录走 IPv6 探测源。
func TestCheckAndUpdate_AAAA(t *testing.T) {
	const v6 = "2408:8207:5410:ef41:8cb6:a48a:7a74:22a2"
	gd := &fakeGoDaddy{records: map[string]string{"AAAA/news": "2408:8207:5410:ef41:8cb6:a48a:7a74:22a1"}}
	svc := newTestService(t, Config{
		Domain: "cozyfish.site", Record: "news", RecordType: RecordTypeAAAA,
		APIKey: "key", APISecret: "secret",
	}, v6+"\n", gd)

	res, err := svc.CheckAndUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckAndUpdate: %v", err)
	}
	if !res.Updated || gd.records["AAAA/news"] != v6 {
		t.Errorf("AAAA 记录未正确更新: %+v / %q", res, gd.records["AAAA/news"])
	}
}

// ip.cn 是两步流程（取 _ticket → 查 JSON），而且必须带浏览器 UA。
// 裸 curl 不带 UA 时页面里没有 _ticket —— 这正是「ip.cn 已失效」这个误判的来源，
// 所以把两步和 UA 要求都锁进测试。
func TestIPCNProvider_TwoStepFlowAndUserAgent(t *testing.T) {
	var indexUA, apiUA string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiUA = r.Header.Get("User-Agent")
		if got := r.URL.Query().Get("ticket"); got != "TICKET-123" {
			_, _ = w.Write([]byte(`{"status":false,"code":1,"msg":"ticket 无效"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":true,"code":0,"msg":"ok","data":{"ip":"123.117.214.162","country":"中国","province":"北京","city":"北京","isp":"联通"}}`))
	}))
	defer api.Close()

	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		indexUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`<html><script>var _ticket = "TICKET-123";</script></html>`))
	}))
	defer index.Close()

	provider := newIPCNProvider(index.URL, api.URL)
	ip, err := provider.fn(context.Background(), mustClient(5*time.Second))
	if err != nil {
		t.Fatalf("ip.cn 两步流程应成功: %v", err)
	}
	if ip != "123.117.214.162" {
		t.Errorf("ip = %q", ip)
	}
	if !strings.Contains(indexUA, "Mozilla/5.0") || !strings.Contains(apiUA, "Mozilla/5.0") {
		t.Errorf("两步都必须带浏览器 UA，实际 index=%q api=%q", indexUA, apiUA)
	}
}

// 页面里没有 _ticket 时必须报错，好让探测回退到下一个源，而不是返回空值。
func TestIPCNProvider_MissingTicket(t *testing.T) {
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>没有 ticket 的页面</html>`))
	}))
	defer index.Close()

	provider := newIPCNProvider(index.URL, "http://127.0.0.1:1/never-called")
	if _, err := provider.fn(context.Background(), mustClient(3*time.Second)); err == nil {
		t.Error("缺少 _ticket 时应报错")
	}
}

// ticket 无效时接口返回 status=false 且没有 data.ip，必须报错。
func TestIPCNProvider_InvalidTicket(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":false,"code":1,"msg":"ticket 无效"}`))
	}))
	defer api.Close()
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`var _ticket = "bad";`))
	}))
	defer index.Close()

	provider := newIPCNProvider(index.URL, api.URL)
	if _, err := provider.fn(context.Background(), mustClient(3*time.Second)); err == nil {
		t.Error("ticket 无效时应报错")
	}
}

// 默认顺序必须把 ip.cn 放在首位（国内直连，不需要代理）。
func TestDefaultIPv4Providers_IPCNFirst(t *testing.T) {
	providers := defaultIPv4Providers()
	if len(providers) == 0 {
		t.Fatal("IPv4 探测源不能为空")
	}
	if providers[0].name != "ip.cn" {
		t.Errorf("首选应为 ip.cn，实际 %q", providers[0].name)
	}
	if len(providers) < 2 {
		t.Error("除 ip.cn 外应保留回退源")
	}
}
