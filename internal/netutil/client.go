// Package netutil 统一构造带代理的出网 HTTP 客户端，供 TMDB 与 Telegram 复用。
package netutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ProxyURL 把配置中的代理地址规范化为 URL。
// 未启用时返回 (nil, nil)。地址可写 127.0.0.1:7890（默认 socks5）
// 或带协议头的 socks5://host:port、http://host:port。
func ProxyURL(addr string, username, password string, enable bool) (*url.URL, error) {
	if !enable || strings.TrimSpace(addr) == "" {
		return nil, nil
	}
	raw := strings.TrimSpace(addr)
	if !strings.Contains(raw, "://") {
		raw = "socks5://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("代理地址解析失败：%w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("代理地址缺少主机名：%s", addr)
	}
	if username != "" {
		u.User = url.UserPassword(username, password)
	}
	return u, nil
}

// TestResult 是一次连通性测试的结果，供设置页展示。
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
	Elapsed int64  `json:"elapsed_ms"`
}

// TestProxy 通过代理访问外网，验证代理可用并返回出口 IP（拿不到不影响结论）。
func TestProxy(ctx context.Context, addr, username, password string, enable bool) TestResult {
	start := time.Now()
	if !enable || strings.TrimSpace(addr) == "" {
		return TestResult{Message: "代理未启用"}
	}
	hc, err := HTTPClient(addr, username, password, true, 20*time.Second)
	if err != nil {
		return TestResult{Message: "代理地址有误", Detail: err.Error()}
	}
	probe := "https://api.telegram.org"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe, nil)
	if err != nil {
		return TestResult{Message: "构造探测请求失败", Detail: err.Error()}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return TestResult{Message: "通过代理访问 " + probe + " 失败", Detail: err.Error(), Elapsed: msSince(start)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	detail := ""
	if ip := outboundIP(ctx, hc); ip != "" {
		detail = "出口 IP：" + ip
	}
	return TestResult{OK: true, Message: "代理可用，已连通 Telegram（HTTP " + strconv.Itoa(resp.StatusCode) + "）", Detail: detail, Elapsed: msSince(start)}
}

func outboundIP(ctx context.Context, hc *http.Client) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org?format=json", nil)
	if err != nil {
		return ""
	}
	resp, err := hc.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ""
	}
	return out.IP
}

func msSince(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

// HTTPClient 按代理配置构造客户端。未启用代理时返回默认传输。
func HTTPClient(addr string, username, password string, enable bool, timeout time.Duration) (*http.Client, error) {
	u, err := ProxyURL(addr, username, password, enable)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	tr := &http.Transport{}
	if u != nil {
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}
