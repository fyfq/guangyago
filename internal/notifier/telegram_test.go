package notifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/odin/gygo/internal/config"
	"github.com/odin/gygo/internal/model"
	"github.com/odin/gygo/internal/ratelimit"
)

func TestCaption_Basic(t *testing.T) {
	l := &model.Link{
		Title: "流浪地球2", Year: 2023,
		Overview: "太阳即将毁灭，人类在地球表面建造出巨大的推进器。",
		URL:      "https://guangyapan.com/s/abc_def",
		Code:     "kk1", TMDBID: 842675, MediaType: model.MediaMovie,
	}
	got := Caption(l)

	if !strings.Contains(got, "<b>流浪地球2</b>") {
		t.Errorf("标题未加粗: %q", got)
	}
	if !strings.Contains(got, "\n\n2023\n\n") {
		t.Errorf("年份应单独成块: %q", got)
	}
	if !strings.Contains(got, `<a href="https://guangyapan.com/s/abc_def">光鸭资源链接</a>`) {
		t.Errorf("分享链接未转成超链接: %q", got)
	}
	if !strings.Contains(got, "提取码：kk1") {
		t.Errorf("缺少提取码: %q", got)
	}
	if !strings.Contains(got, "https://www.themoviedb.org/movie/842675") {
		t.Errorf("TMDB 链接应按 movie 生成: %q", got)
	}
}

func TestCaption_Fallbacks(t *testing.T) {
	// 没有 title 时退回 raw_title
	got := Caption(&model.Link{RawTitle: "原始片名", URL: "https://a.com/s/x"})
	if !strings.Contains(got, "<b>原始片名</b>") {
		t.Errorf("应退回 raw_title: %q", got)
	}
	// 无年份 / 无提取码 / 无 TMDB 时不应出现对应块
	if strings.Contains(got, "提取码") || strings.Contains(got, "TMDB") || strings.Contains(got, "\n\n0\n\n") {
		t.Errorf("空字段不应输出: %q", got)
	}

	// 剧集走 tv 路径
	got = Caption(&model.Link{Title: "权游", TMDBID: 1399, MediaType: model.MediaTV, URL: "https://a.com"})
	if !strings.Contains(got, "https://www.themoviedb.org/tv/1399") {
		t.Errorf("tv 类型 TMDB 链接错误: %q", got)
	}
}

func TestCaption_EscapesHTML(t *testing.T) {
	got := Caption(&model.Link{Title: `<script>alert("x")</script>`, Code: `a"b`, URL: "https://a.com"})
	if strings.Contains(got, "<script>") {
		t.Errorf("标题未转义: %q", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("应输出转义后的标题: %q", got)
	}
	if strings.Contains(got, `a"b`) {
		t.Errorf("提取码未转义: %q", got)
	}
}

func TestCaption_LinksOnlyHTTP(t *testing.T) {
	// javascript: 不应被包成超链接
	got := Caption(&model.Link{Title: "t", URL: "javascript:alert(1)"})
	if strings.Contains(got, "<a href=") {
		t.Errorf("非 http(s) 链接不应生成超链接: %q", got)
	}
	if !strings.Contains(got, linkLabel) {
		t.Errorf("纯文本链接文案缺失: %q", got)
	}
}

func TestCaption_TruncatesOverview(t *testing.T) {
	long := strings.Repeat("很长", 300)
	got := Caption(&model.Link{Title: "t", Overview: long, URL: "https://a.com"})
	if !strings.Contains(got, "…") {
		t.Error("超长简介应补省略号")
	}
	// 简介块最多 overviewLimit 个字符，整体文案应在上限内
	if n := len([]rune(got)); n > captionLimit {
		t.Errorf("文案长度 %d 超过上限 %d", n, captionLimit)
	}
}

func TestCaption_CollapsesWhitespace(t *testing.T) {
	got := Caption(&model.Link{Title: "t", Overview: "第一行\r\n\r\n第二行\t尾部", URL: "https://a.com"})
	if strings.Contains(got, "\r") || strings.Contains(got, "\t") {
		t.Errorf("简介中的换行/制表符应被折叠: %q", got)
	}
	if strings.Contains(got, "第一行  第二行") && strings.Count(got, "  ") > 0 {
		// 折叠后应是单个空格
	}
	if !strings.Contains(got, "第一行 第二行 尾部") {
		t.Errorf("简介折叠结果异常: %q", got)
	}
}

func TestOneLineTruncateRunes(t *testing.T) {
	if got := oneLine("  a \n\t b  "); got != "a b" {
		t.Errorf("oneLine = %q, 期望 \"a b\"", got)
	}
	if got := truncateRunes("abcdef", 3); got != "ab…" {
		t.Errorf("truncateRunes(6,3) = %q", got)
	}
	if got := truncateRunes("abc", 3); got != "abc" {
		t.Errorf("未超长不应截断: %q", got)
	}
	if got := truncateRunes("abcdef", 1); got != "" {
		t.Errorf("limit<=1 应返回空串，实际 %q", got)
	}
	if got := truncateRunes("abcdef", 0); got != "" {
		t.Errorf("limit=0 应返回空串，实际 %q", got)
	}
}

func TestTMDBPathAndIsHTTPURL(t *testing.T) {
	if tmdbPath("tv") != "tv" || tmdbPath("movie") != "movie" || tmdbPath("") != "movie" {
		t.Error("tmdbPath 判定错误")
	}
	if !isHTTPURL("https://a.com/x") || !isHTTPURL("http://a.com") {
		t.Error("http(s) 应识别为合法链接")
	}
	if isHTTPURL("javascript:alert(1)") || isHTTPURL("//a.com") || isHTTPURL("") {
		t.Error("非法链接不应通过")
	}
}

func TestNew_EmptyToken(t *testing.T) {
	n, err := New(config.TelegramConfig{}, nil, nil)
	if err != nil || n != nil {
		t.Errorf("空 Token 应返回 nil,nil，实际 %v,%v", n, err)
	}
}

func TestNew_BadProxyAddr(t *testing.T) {
	if _, err := New(config.TelegramConfig{Token: "1:test"},
		&config.ProxyConfig{Enable: true, Addr: "socks5://"}, nil); err == nil {
		t.Error("非法代理地址应返回错误")
	}
}

func TestNewLimiter(t *testing.T) {
	// nil 走默认配置
	l := newLimiter(nil)
	if l == nil {
		t.Fatal("newLimiter(nil) 不应返回 nil")
	}
	if err := l.Wait(context.Background()); err != nil {
		t.Errorf("默认限流器首次 Wait 应成功: %v", err)
	}

	rate := config.TelegramRate{Enable: true, IntervalMS: 1}
	if l := newLimiter(&rate); l == nil {
		t.Error("启用限流时不应返回 nil")
	}
	// 关闭限流后 Wait 立即返回
	rate.Enable = false
	if l := newLimiter(&rate); l == nil {
		t.Error("禁用限流时也应返回限流器")
	}
}

func TestSend_NotConfigured(t *testing.T) {
	var n *Notifier
	if _, err := n.Send(context.Background(), &model.Link{}); err == nil {
		t.Error("nil Notifier 应报错")
	}

	// bot 为空
	empty := &Notifier{}
	if _, err := empty.Send(context.Background(), &model.Link{}); err == nil {
		t.Error("未配置 bot 应报错")
	}
}

func TestTestTelegram_EmptyToken(t *testing.T) {
	r := TestTelegram(context.Background(), config.TelegramConfig{}, nil)
	if r.OK || !strings.Contains(r.Message, "Bot Token") {
		t.Errorf("空 Token 应提示未填写: %+v", r)
	}
}

func TestTestTelegram_BadProxy(t *testing.T) {
	r := TestTelegram(context.Background(), config.TelegramConfig{Token: "1:test"},
		&config.ProxyConfig{Enable: true, Addr: "socks5://"})
	if r.OK || !strings.Contains(r.Message, "Bot 初始化失败") {
		t.Errorf("非法代理应提示初始化失败: %+v", r)
	}
}

// newBot 构造时会真实调用 getMe 校验 Token，依赖外网，不放在单元测试里。

// fakeTelegram 起一个最小可用的 Telegram API 端点，供 Send 的成功与回退分支使用。
func fakeTelegram(t *testing.T, photoOK bool) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		calls = append(calls, method)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "sendPhoto":
			if !photoOK {
				_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"wrong file"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42,"date":0,"chat":{"id":1,"type":"channel"}}}`))
		case "sendMessage":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":43,"date":0,"chat":{"id":1,"type":"channel"}}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newFakeNotifier(t *testing.T, srv *httptest.Server) *Notifier {
	t.Helper()
	b, err := bot.New("1:test", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("构造 bot: %v", err)
	}
	return &Notifier{bot: b, chatID: int64(1), limiter: ratelimit.New(10000, 10, true)}
}

func TestSend_Success(t *testing.T) {
	srv, calls := fakeTelegram(t, true)
	n := newFakeNotifier(t, srv)
	id, err := n.Send(context.Background(), &model.Link{
		Title: "t", PosterURL: "https://img/a.jpg", URL: "https://a.com",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if id != 42 {
		t.Errorf("message id = %d, 期望 42", id)
	}
	if len(*calls) != 1 || (*calls)[0] != "sendPhoto" {
		t.Errorf("应只调用一次 sendPhoto，实际 %v", *calls)
	}
}

func TestSend_FallsBackToText(t *testing.T) {
	srv, calls := fakeTelegram(t, false)
	n := newFakeNotifier(t, srv)
	id, err := n.Send(context.Background(), &model.Link{
		Title: "t", PosterURL: "https://img/bad.jpg", URL: "https://a.com",
	})
	if err != nil {
		t.Fatalf("Send 回退失败: %v", err)
	}
	if id != 43 {
		t.Errorf("回退后的 message id = %d, 期望 43", id)
	}
	if len(*calls) != 2 || (*calls)[0] != "sendPhoto" || (*calls)[1] != "sendMessage" {
		t.Errorf("海报失败后应回退到 sendMessage，实际 %v", *calls)
	}
}

func TestSend_MissingChatID(t *testing.T) {
	srv, _ := fakeTelegram(t, true)
	n := newFakeNotifier(t, srv)
	n.chatID = nil
	if _, err := n.Send(context.Background(), &model.Link{Title: "t"}); err == nil {
		t.Error("缺少频道 ID 应报错")
	}
}

func TestSince(t *testing.T) {
	if d := since(time.Now().Add(-time.Second)); d < 900 || d > 2000 {
		t.Errorf("since 结果异常: %d", d)
	}
}

// 令牌耗尽且 ctx 已取消时，Send 应在取令牌阶段就返回错误，不发出任何请求。
func TestSend_LimiterCanceled(t *testing.T) {
	srv, calls := fakeTelegram(t, true)
	n := newFakeNotifier(t, srv)
	n.limiter = ratelimit.New(0.001, 1, true)
	if err := n.limiter.Wait(context.Background()); err != nil {
		t.Fatalf("预取令牌失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.Send(ctx, &model.Link{Title: "t", URL: "https://a.com"}); err == nil {
		t.Error("取令牌被取消时应返回错误")
	}
	if len(*calls) != 0 {
		t.Errorf("未拿到令牌不应发出请求: %v", *calls)
	}
}

// 海报发送失败后回退文本，回退同样要过限流；此时 ctx 已取消应立即失败。
func TestSend_FallbackLimiterCanceled(t *testing.T) {
	srv, _ := fakeTelegram(t, true)
	n := newFakeNotifier(t, srv)
	n.limiter = ratelimit.New(0.001, 1, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.Send(ctx, &model.Link{
		Title: "t", PosterURL: "https://img/a.jpg", URL: "https://a.com",
	}); err == nil {
		t.Error("回退取令牌被取消时应返回错误")
	}
}

func TestPhotoInput(t *testing.T) {
	for _, raw := range []string{"", "data/posters/movie-550.jpg", "/data/posters/movie-550.jpg", "AgACAgIAAxk"} {
		if p := photoInput(raw); p != nil {
			t.Errorf("非 http(s) 值 %q 不应生成图片参数: %+v", raw, p)
		}
	}
	if p := photoInput("https://image.tmdb.org/t/p/w500/a.jpg"); p == nil || p.Data == "" {
		t.Error("http(s) 地址应生成图片参数")
	}
}

// 没有海报、或海报不是 http(s) 地址时直接发纯文本：
// 这类值交给 Telegram 会被当成 file_id，报 wrong remote file identifier。
func TestSend_NoOrBadPosterUsesText(t *testing.T) {
	for _, poster := range []string{"", "data/posters/movie-550.jpg"} {
		srv, calls := fakeTelegram(t, true)
		n := newFakeNotifier(t, srv)
		id, err := n.Send(context.Background(), &model.Link{
			Title: "t", PosterURL: poster, URL: "https://a.com",
		})
		if err != nil {
			t.Fatalf("PosterURL=%q 发送失败: %v", poster, err)
		}
		if id != 43 {
			t.Errorf("PosterURL=%q 消息 ID = %d，期望 43", poster, id)
		}
		if len(*calls) != 1 || (*calls)[0] != "sendMessage" {
			t.Errorf("PosterURL=%q 应只调用 sendMessage，实际 %v", poster, *calls)
		}
	}
}
