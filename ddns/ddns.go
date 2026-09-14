// Package ddns keeps a GoDaddy DNS record pointed at this machine's public IP.
//
// It is a port of the video-server project's pkg/ddns, with three changes that
// matter for the news site:
//
//  1. IP detection no longer scrapes ip.cn (that ticket flow stopped working,
//     which left cozyfish.site pointing at a stale address). It now tries a
//     list of plain-text echo providers with fallback.
//  2. AAAA is supported, not just A. This machine has a public IPv6 address and
//     an IPv4 that may sit behind carrier NAT, so the record type is a choice.
//  3. The update result is propagated. The original logged a failed PUT and
//     still reported success to its caller.
//
// Detection deliberately never uses the proxy: a proxy reports its own egress
// address, so publishing that would point the domain at the proxy, not at this
// machine. The GoDaddy API call does use the proxy, since it is reachable only
// from outside the mainland network.
package ddns

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RecordTypeA / RecordTypeAAAA are the supported record types.
const (
	RecordTypeA    = "A"
	RecordTypeAAAA = "AAAA"
)

// ipProvider 返回本机在互联网上的出口地址。
type ipProvider struct {
	name string
	fn   func(ctx context.Context, client *http.Client) (string, error)
}

const (
	ipcnIndexURL = "https://ip.cn"
	ipcnAPIURL   = "https://my.ip.cn/json/"
)

var ticketRe = regexp.MustCompile(`var _ticket = "([^"]+)"`)

// browserUA 是 ip.cn 的硬要求：不带浏览器 UA 时返回的页面里没有 _ticket，
// 用裸 curl 去测就会得出「ip.cn 已失效」的错误结论——它并没有失效。
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"

// defaultIPv4Providers 按顺序尝试。ip.cn 放首位：国内直连、不需要代理，
// 而且会一并返回归属地。
func defaultIPv4Providers() []ipProvider {
	return []ipProvider{
		newIPCNProvider(ipcnIndexURL, ipcnAPIURL),
		newPlainProvider("api-ipv4.ip.sb", "https://api-ipv4.ip.sb/ip"),
		newPlainProvider("ipv4.icanhazip.com", "https://ipv4.icanhazip.com"),
		newPlainProvider("3322.org", "http://members.3322.org/dyndns/getip"),
	}
}

func defaultIPv6Providers() []ipProvider {
	return []ipProvider{
		newPlainProvider("api-ipv6.ip.sb", "https://api-ipv6.ip.sb/ip"),
		newPlainProvider("v6.ident.me", "https://v6.ident.me"),
	}
}

// newIPCNProvider 走 ip.cn 的两步流程：先从页面取 _ticket，再查 JSON 接口。
func newIPCNProvider(indexURL, apiURL string) ipProvider {
	return ipProvider{
		name: "ip.cn",
		fn: func(ctx context.Context, client *http.Client) (string, error) {
			page, err := fetchText(ctx, client, indexURL)
			if err != nil {
				return "", err
			}
			ticket := parseIPCNTicket(page)
			if ticket == "" {
				return "", fmt.Errorf("页面里没有 _ticket（可能被风控或改版）")
			}
			body, err := fetchText(ctx, client, apiURL+"?ticket="+url.QueryEscape(ticket))
			if err != nil {
				return "", err
			}
			ip := parseIPCNResponse(body)
			if ip == "" {
				return "", fmt.Errorf("响应里没有 data.ip: %s", truncate(body, 80))
			}
			return ip, nil
		},
	}
}

func newPlainProvider(name, endpoint string) ipProvider {
	return ipProvider{
		name: name,
		fn: func(ctx context.Context, client *http.Client) (string, error) {
			body, err := fetchText(ctx, client, endpoint)
			if err != nil {
				return "", err
			}
			ip := extractIP(body)
			if ip == "" {
				return "", fmt.Errorf("响应中没有 IP: %s", truncate(body, 80))
			}
			return ip, nil
		},
	}
}

// parseIPCNTicket 从页面里取 _ticket，ip.cn 用它把两次请求绑在一起。
func parseIPCNTicket(page string) string {
	if m := ticketRe.FindStringSubmatch(page); len(m) >= 2 {
		return m[1]
	}
	return ""
}

// parseIPCNResponse 取 data.ip；该接口同时返回 country/province/city/isp。
func parseIPCNResponse(body string) string {
	var out struct {
		Data struct {
			IP string `json:"ip"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return ""
	}
	return strings.TrimSpace(out.Data.IP)
}

// fetchText 统一使用浏览器 UA：ip.cn 需要它，其余回显源不受影响。
func fetchText(ctx context.Context, client *http.Client, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	return string(out), nil
}

const godaddyAPIBase = "https://api.godaddy.com"

// Config holds everything needed to keep one DNS record current.
type Config struct {
	Domain        string
	Record        string // "@" 表示根域
	RecordType    string // A 或 AAAA
	APIKey        string
	APISecret     string
	TTL           int
	CheckInterval time.Duration
	ProxyAddr     string // 仅用于访问 GoDaddy API
}

// ConfigFromEnv reads the DDNS_* variables. enabled is false when the feature is
// switched off, in which case the returned error is nil and the caller should
// simply skip it. Credentials never come from a file inside the repository:
// .env is gitignored, so secrets cannot be committed by accident.
func ConfigFromEnv() (cfg Config, enabled bool, err error) {
	if !truthy(os.Getenv("DDNS_ENABLED")) {
		return Config{}, false, nil
	}
	cfg = Config{
		Domain:     strings.TrimSpace(os.Getenv("DDNS_DOMAIN")),
		Record:     strings.TrimSpace(os.Getenv("DDNS_RECORD")),
		RecordType: strings.ToUpper(strings.TrimSpace(os.Getenv("DDNS_RECORD_TYPE"))),
		APIKey:     strings.TrimSpace(os.Getenv("DDNS_API_KEY")),
		APISecret:  strings.TrimSpace(os.Getenv("DDNS_API_SECRET")),
		ProxyAddr:  strings.TrimSpace(os.Getenv("DDNS_PROXY")),
	}
	if v := strings.TrimSpace(os.Getenv("DDNS_TTL")); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			return Config{}, true, fmt.Errorf("DDNS_TTL 不是整数: %q", v)
		}
		cfg.TTL = n
	}
	if v := strings.TrimSpace(os.Getenv("DDNS_CHECK_INTERVAL")); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			return Config{}, true, fmt.Errorf("DDNS_CHECK_INTERVAL 不是整数: %q", v)
		}
		cfg.CheckInterval = time.Duration(n) * time.Second
	}
	if err := cfg.applyDefaults(); err != nil {
		return Config{}, true, err
	}
	return cfg, true, nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Resolve fills in defaults and validates. Split out so tests can build a
// Config directly without touching the environment.
func (c *Config) applyDefaults() error {
	if c.Record == "" {
		c.Record = "@"
	}
	if c.RecordType == "" {
		c.RecordType = RecordTypeA
	}
	if c.TTL < 600 {
		// GoDaddy 不接受低于 600 的 TTL
		c.TTL = 600
	}
	if c.CheckInterval <= 0 {
		c.CheckInterval = 5 * time.Minute
	}
	switch c.RecordType {
	case RecordTypeA, RecordTypeAAAA:
	default:
		return fmt.Errorf("DDNS_RECORD_TYPE 只能是 A 或 AAAA，收到 %q", c.RecordType)
	}
	for name, v := range map[string]string{
		"DDNS_DOMAIN":     c.Domain,
		"DDNS_API_KEY":    c.APIKey,
		"DDNS_API_SECRET": c.APISecret,
	} {
		if v == "" {
			return fmt.Errorf("%s 不能为空", name)
		}
	}
	return nil
}

// Service performs one check-and-update cycle or runs them on an interval.
type Service struct {
	cfg Config
	// detectClient 不使用代理：必须看到本机真实出口 IP
	detectClient *http.Client
	// apiClient 使用代理：api.godaddy.com 在墙外
	apiClient *http.Client
	// 以下三项在 New 里取默认值，测试可替换成本地 httptest 服务
	ipv4Providers []ipProvider
	ipv6Providers []ipProvider
	apiBase       string
}

// New validates cfg and builds the two HTTP clients.
func New(cfg Config) (*Service, error) {
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	apiClient, err := newClient(30*time.Second, cfg.ProxyAddr)
	if err != nil {
		return nil, err
	}
	return &Service{
		cfg:           cfg,
		detectClient:  mustClient(10 * time.Second),
		apiClient:     apiClient,
		ipv4Providers: defaultIPv4Providers(),
		ipv6Providers: defaultIPv6Providers(),
		apiBase:       godaddyAPIBase,
	}, nil
}

// Result reports what one cycle observed and whether it changed anything.
type Result struct {
	PublicIP string
	DNSIP    string
	Updated  bool
}

// CheckAndUpdate compares the machine's public IP with the DNS record and
// updates the record when they differ.
func (s *Service) CheckAndUpdate(ctx context.Context) (Result, error) {
	publicIP, err := s.detectPublicIP(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{PublicIP: publicIP}

	dnsIP, err := s.getRecordIP(ctx)
	if err != nil {
		return res, err
	}
	res.DNSIP = dnsIP

	if publicIP == dnsIP {
		log.Printf("[DDNS] %s.%s 已是最新: %s", s.cfg.Record, s.cfg.Domain, publicIP)
		return res, nil
	}
	log.Printf("[DDNS] IP 变更 %s → %s，更新 %s 记录…",
		displayOr(dnsIP, "（无记录）"), publicIP, s.cfg.RecordType)
	if err := s.putRecordIP(ctx, publicIP); err != nil {
		return res, err
	}
	res.Updated = true
	log.Printf("[DDNS] 已更新 %s.%s → %s", s.cfg.Record, s.cfg.Domain, publicIP)
	return res, nil
}

// Run checks once immediately, then every CheckInterval until ctx is done.
func (s *Service) Run(ctx context.Context) {
	log.Printf("[DDNS] 启动: %s.%s 类型=%s 间隔=%s",
		s.cfg.Record, s.cfg.Domain, s.cfg.RecordType, s.cfg.CheckInterval)
	for {
		if _, err := s.CheckAndUpdate(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[DDNS] 检查失败: %v", err)
		}
		select {
		case <-ctx.Done():
			log.Printf("[DDNS] 停止")
			return
		case <-time.After(s.cfg.CheckInterval):
		}
	}
}

// detectPublicIP tries each provider for the configured address family. A
// provider that answers with the wrong family is treated as a failure so that a
// dual-stack host cannot silently publish a v6 address into an A record.
func (s *Service) detectPublicIP(ctx context.Context) (string, error) {
	wantV6 := s.cfg.RecordType == RecordTypeAAAA
	providers := s.ipv4Providers
	if wantV6 {
		providers = s.ipv6Providers
	}

	var lastErr error
	for _, provider := range providers {
		ip, err := provider.fn(ctx, s.detectClient)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", provider.name, err)
			continue
		}
		if isV6(ip) != wantV6 {
			lastErr = fmt.Errorf("%s: 地址族不符（需要 %s，得到 %s）", provider.name, s.cfg.RecordType, ip)
			continue
		}
		if !isPublicIP(ip) {
			lastErr = fmt.Errorf("%s: %s 不是公网地址", provider.name, ip)
			continue
		}
		log.Printf("[DDNS] 公网地址来自 %s: %s", provider.name, ip)
		return ip, nil
	}
	return "", fmt.Errorf("所有 IP 探测源均失败: %w", lastErr)
}

func (s *Service) getRecordIP(ctx context.Context) (string, error) {
	endpoint := fmt.Sprintf("%s/v1/domains/%s/records/%s/%s",
		s.apiBase, url.PathEscape(s.cfg.Domain), s.cfg.RecordType, url.PathEscape(s.cfg.Record))

	body, err := s.fetchAuth(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("读取 DNS 记录失败: %w", err)
	}
	var records []struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &records); err != nil {
		return "", fmt.Errorf("解析 DNS 记录失败: %w", err)
	}
	if len(records) == 0 {
		return "", nil
	}
	return strings.TrimSpace(records[0].Data), nil
}

func (s *Service) putRecordIP(ctx context.Context, ip string) error {
	endpoint := fmt.Sprintf("%s/v1/domains/%s/records/%s/%s",
		s.apiBase, url.PathEscape(s.cfg.Domain), s.cfg.RecordType, url.PathEscape(s.cfg.Record))

	payload, err := json.Marshal([]map[string]any{{
		"data": ip,
		"ttl":  s.cfg.TTL,
		"name": s.cfg.Record,
		"type": s.cfg.RecordType,
	}})
	if err != nil {
		return err
	}
	if _, err := s.fetchAuth(ctx, http.MethodPut, endpoint, payload); err != nil {
		return fmt.Errorf("写入 DNS 记录失败: %w", err)
	}
	return nil
}

func (s *Service) fetchAuth(ctx context.Context, method, endpoint string, payload []byte) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		body = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("sso-key %s:%s", s.cfg.APIKey, s.cfg.APISecret))
	req.Header.Set("Content-Type", "application/json")
	return s.do(s.apiClient, req)
}

func (s *Service) do(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	return out, nil
}

func newClient(timeout time.Duration, proxyAddr string) (*http.Client, error) {
	if proxyAddr == "" {
		return mustClient(timeout), nil
	}
	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("DDNS_PROXY 不是合法 URL: %w", err)
	}
	transport := mustTransport()
	transport.Proxy = http.ProxyURL(proxyURL)
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func mustClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: mustTransport(), Timeout: timeout}
}

func mustTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
}

// extractIP returns the first IP literal found anywhere in body. Splitting on
// every character that cannot appear in an IP keeps this working for plain-text
// endpoints and for pages that embed the address in prose.
func extractIP(body string) string {
	fields := strings.FieldsFunc(body, func(r rune) bool {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			return false
		case r == '.' || r == ':':
			return false
		}
		return true
	})
	for _, f := range fields {
		f = strings.Trim(f, ".:")
		if f == "" {
			continue
		}
		if ip := net.ParseIP(f); ip != nil {
			return ip.String()
		}
	}
	return ""
}

func isV6(ip string) bool { return strings.Contains(ip, ":") }

// isPublicIP rejects anything that must not end up in a public DNS record:
// unspecified, loopback, link-local, and RFC1918/ULA private ranges.
func isPublicIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.IsLoopback() || parsed.IsUnspecified() ||
		parsed.IsLinkLocalUnicast() || parsed.IsLinkLocalMulticast() ||
		parsed.IsPrivate() {
		return false
	}
	return true
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func displayOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
