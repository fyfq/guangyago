package netutil

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProxyURL(t *testing.T) {
	// 未启用或空地址返回 nil
	for _, c := range []struct {
		addr       string
		user, pass string
		enable     bool
	}{
		{"127.0.0.1:7890", "", "", false},
		{"", "", "", true},
		{"   ", "", "", true},
	} {
		u, err := ProxyURL(c.addr, c.user, c.pass, c.enable)
		if err != nil || u != nil {
			t.Errorf("ProxyURL(%q,%v) = %v,%v，期望 nil,nil", c.addr, c.enable, u, err)
		}
	}

	// 裸地址默认补 socks5
	u, err := ProxyURL("127.0.0.1:7890", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "socks5" || u.Host != "127.0.0.1:7890" {
		t.Errorf("裸地址应补 socks5: %v", u)
	}

	// 带协议头原样保留
	u, err = ProxyURL("http://proxy.local:8080", "", "", true)
	if err != nil || u.Scheme != "http" {
		t.Errorf("带协议头解析异常: %v, err=%v", u, err)
	}

	// 用户名密码写入 userinfo
	u, err = ProxyURL("socks5://h:1080", "bob", "s3cret", true)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := u.User.Password(); p != "s3cret" || u.User.Username() != "bob" {
		t.Errorf("代理认证信息未设置: %v", u.User)
	}

	// 缺少主机名应报错
	if _, err := ProxyURL("socks5://", "", "", true); err == nil {
		t.Error("缺少主机名应报错")
	}
}

func TestHTTPClient(t *testing.T) {
	// 未启用代理：挂默认 Transport，超时按传入值
	hc, err := HTTPClient("", "", "", false, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if hc.Timeout != 3*time.Second {
		t.Errorf("timeout = %v, 期望 3s", hc.Timeout)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型异常: %T", hc.Transport)
	}
	if tr.Proxy != nil {
		t.Error("未启用代理时不应设置 Proxy")
	}
	if tr.MaxConnsPerHost != 16 || tr.MaxIdleConnsPerHost != 8 {
		t.Errorf("连接池参数异常: max=%d idle=%d", tr.MaxConnsPerHost, tr.MaxIdleConnsPerHost)
	}

	// timeout<=0 回落到默认 20s
	hc, err = HTTPClient("", "", "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hc.Timeout != 20*time.Second {
		t.Errorf("零超时应回落 20s，实际 %v", hc.Timeout)
	}

	// 启用代理：Proxy 函数非空
	hc, err = HTTPClient("127.0.0.1:7890", "", "", true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if hc.Transport.(*http.Transport).Proxy == nil {
		t.Error("启用代理时应设置 Proxy")
	}

	// 非法代理地址透传错误
	if _, err := HTTPClient("socks5://", "", "", true, time.Second); err == nil {
		t.Error("非法代理地址应报错")
	}
}

func TestTestProxy_DisabledAndBadAddr(t *testing.T) {
	if r := TestProxy(context.Background(), "", "", "", false); r.OK || r.Message != "代理未启用" {
		t.Errorf("未启用时应提示代理未启用: %+v", r)
	}
	if r := TestProxy(context.Background(), "127.0.0.1:7890", "", "", false); r.OK || r.Message != "代理未启用" {
		t.Errorf("enable=false 时应提示代理未启用: %+v", r)
	}
	if r := TestProxy(context.Background(), "socks5://", "", "", true); r.OK || r.Message != "代理地址有误" {
		t.Errorf("非法地址应提示地址有误: %+v", r)
	}
}

// 把代理指向一个必然拒绝连接的端口，验证失败分支返回结构化结果而不是 panic。
func TestTestProxy_Unreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // 立即关闭，端口不可连接

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := TestProxy(ctx, addr, "", "", true)
	if r.OK {
		t.Errorf("代理不可达时不应返回 OK: %+v", r)
	}
	if r.Message == "" {
		t.Error("失败时应给出提示文案")
	}
}

func TestOutboundIP_BadClient(t *testing.T) {
	// 指向不可达地址的客户端拿不到出口 IP，应返回空串而不是 panic
	hc := &http.Client{Timeout: 50 * time.Millisecond}
	if ip := outboundIP(context.Background(), hc); ip != "" {
		t.Errorf("不可达网络应返回空 IP，实际 %q", ip)
	}
}

// roundTripFunc 把任意请求固定到本地响应，避免单测依赖外网。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeIPClient(body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
}

func TestOutboundIP_Success(t *testing.T) {
	if ip := outboundIP(context.Background(), fakeIPClient(`{"ip":"203.0.113.9"}`)); ip != "203.0.113.9" {
		t.Errorf("出口 IP 解析异常: %q", ip)
	}
}

func TestOutboundIP_BadJSON(t *testing.T) {
	if ip := outboundIP(context.Background(), fakeIPClient("not json")); ip != "" {
		t.Errorf("非法响应应返回空 IP，实际 %q", ip)
	}
}

func TestMsSince(t *testing.T) {
	if d := msSince(time.Now().Add(-time.Second)); d < 900 || d > 2000 {
		t.Errorf("msSince 结果异常: %d", d)
	}
}
