package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/odin/guangya-go/internal/config"
)

// seamLog 记录假 Bot API 收到的调用顺序，并把 sendMessage 的正文送进 Sent 通道。
type seamLog struct {
	mu    sync.Mutex
	calls []string
	Sent  chan string
}

func (l *seamLog) methods() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// seamTelegram 起一个假的 Telegram Bot API。respond(method, call) 返回该方法的响应体，
// call 从 1 开始计数（同一次构造 bot 会先调一次 getMe）；返回空串表示用默认的 ok。
func seamTelegram(t *testing.T, respond func(method string, call int) string) (*httptest.Server, *seamLog) {
	t.Helper()
	log := &seamLog{Sent: make(chan string, 8)}
	var mu sync.Mutex
	seq := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		seq[method]++
		call := seq[method]
		mu.Unlock()

		log.mu.Lock()
		log.calls = append(log.calls, method)
		log.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "sendMessage":
			text := r.FormValue("text")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":99,"date":0,"chat":{"id":1,"type":"private"}}}`))
			log.Sent <- text
			return
		case "sendPhoto":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42,"date":0,"chat":{"id":1,"type":"channel"}}}`))
			return
		}
		if body := respond(method, call); body != "" {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

const (
	seamGetMe        = `{"ok":true,"result":{"id":77,"is_bot":true,"first_name":"光鸭","username":"guangya_bot"}}`
	seamGetChat      = `{"ok":true,"result":{"id":-1001234567890,"type":"channel","title":"光鸭资源分享"}}`
	seamChatAdmin    = `{"ok":true,"result":{"status":"administrator","user":{"id":77,"is_bot":true,"first_name":"光鸭","username":"guangya_bot"}}}`
	seamChatMember   = `{"ok":true,"result":{"status":"member","user":{"id":77,"is_bot":true,"first_name":"光鸭","username":"guangya_bot"}}}`
	seamUnauthorized = `{"ok":false,"error_code":401,"description":"Unauthorized"}`
)

// seamAdmin 给出「Token 正常 + Bot 是频道管理员」的标准应答，测试在其上覆盖单个分支。
func seamAdmin(method string, _ int) string {
	switch method {
	case "getMe":
		return seamGetMe
	case "getChat":
		return seamGetChat
	case "getChatMember":
		return seamChatAdmin
	}
	return ""
}

func seamConfig(chatID config.ChatID) config.TelegramConfig {
	return config.TelegramConfig{Token: "77:secret-token", ChatID: chatID}
}

func TestNewAtServerURL_UsesInjectedServer(t *testing.T) {
	srv, log := seamTelegram(t, seamAdmin)
	n, err := NewAtServerURL(seamConfig("-1001234567890"), nil, nil, srv.URL)
	if err != nil {
		t.Fatalf("NewAtServerURL: %v", err)
	}
	if n == nil {
		t.Fatal("配置了 Token 不应返回 nil")
	}
	// 构造时会真实调用一次 getMe，这正好证明请求打到了注入的地址而不是 api.telegram.org
	if got := log.methods(); len(got) != 1 || got[0] != "getMe" {
		t.Errorf("构造应只调用 getMe，实际 %v", got)
	}
}

func TestNewAtServerURL_EmptyToken(t *testing.T) {
	n, err := NewAtServerURL(config.TelegramConfig{}, nil, nil, "https://example.invalid")
	if err != nil || n != nil {
		t.Errorf("空 Token 应返回 (nil, nil)，实际 %+v %v", n, err)
	}
}

func TestTestTelegramAt_OK(t *testing.T) {
	srv, _ := seamTelegram(t, seamAdmin)
	got := testTelegramAt(context.Background(), seamConfig("-1001234567890"), nil, srv.URL)
	if !got.OK {
		t.Fatalf("应判定成功: %+v", got)
	}
	if !strings.Contains(got.Message, "@guangya_bot") || !strings.Contains(got.Message, "光鸭资源分享") {
		t.Errorf("文案应带上 Bot 用户名与频道名: %+v", got)
	}
	if !strings.Contains(got.Detail, "administrator") {
		t.Errorf("详情应给出 Bot 身份: %+v", got)
	}
}

func TestTestTelegramAt_NotAdmin(t *testing.T) {
	srv, _ := seamTelegram(t, func(method string, _ int) string {
		if method == "getChatMember" {
			return seamChatMember
		}
		return seamAdmin(method, 0)
	})
	got := testTelegramAt(context.Background(), seamConfig("@guangya"), nil, srv.URL)
	if got.OK || !strings.Contains(got.Message, "不是管理员") {
		t.Errorf("非管理员应提示无法发消息: %+v", got)
	}
}

func TestTestTelegramAt_GetChatFails(t *testing.T) {
	srv, _ := seamTelegram(t, func(method string, _ int) string {
		if method == "getChat" {
			return `{"ok":false,"error_code":400,"description":"chat not found"}`
		}
		return seamAdmin(method, 0)
	})
	got := testTelegramAt(context.Background(), seamConfig("-1001234567890"), nil, srv.URL)
	if got.OK || !strings.Contains(got.Message, "读取频道失败") {
		t.Errorf("频道读取失败应单独提示: %+v", got)
	}
}

func TestTestTelegramAt_NoChatID(t *testing.T) {
	srv, log := seamTelegram(t, seamAdmin)
	got := testTelegramAt(context.Background(), seamConfig(""), nil, srv.URL)
	if !got.OK || !strings.Contains(got.Detail, "未填频道 ID") {
		t.Errorf("未填频道 ID 应只验证 Token: %+v", got)
	}
	for _, m := range log.methods() {
		if m == "getChat" {
			t.Error("未填频道 ID 不应请求 getChat")
		}
	}
}

// 构造 bot 时 getMe 就失败，走 newBot 的错误分支。
func TestTestTelegramAt_InitFails(t *testing.T) {
	srv, _ := seamTelegram(t, func(string, int) string { return seamUnauthorized })
	got := testTelegramAt(context.Background(), seamConfig("-1001234567890"), nil, srv.URL)
	if got.OK || !strings.Contains(got.Message, "Bot 初始化失败") {
		t.Errorf("Token 无效应在构造阶段失败: %+v", got)
	}
}

// 构造时的 getMe 通过、测试里的第二次 getMe 失败：覆盖「Token 无效或无法连接」分支。
func TestTestTelegramAt_GetMeFails(t *testing.T) {
	srv, _ := seamTelegram(t, func(method string, call int) string {
		if method == "getMe" && call == 2 {
			return seamUnauthorized
		}
		return seamAdmin(method, call)
	})
	got := testTelegramAt(context.Background(), seamConfig("-1001234567890"), nil, srv.URL)
	if got.OK || !strings.Contains(got.Message, "Bot Token 无效") {
		t.Errorf("getMe 失败应提示 Token 无效: %+v", got)
	}
}

func TestTestTelegramAt_BadProxy(t *testing.T) {
	got := testTelegramAt(context.Background(), seamConfig(""), &config.ProxyConfig{Enable: true, Addr: "socks5://"}, "https://example.invalid")
	if got.OK || !strings.Contains(got.Message, "Bot 初始化失败") {
		t.Errorf("非法代理应提示初始化失败: %+v", got)
	}
}

// seamUpdateBatch 造一条「私聊里发来的入库消息」，文本按 JSON 字符串正确转义。
func seamUpdateBatch(t *testing.T, text string) string {
	t.Helper()
	msg, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("编码入站文本: %v", err)
	}
	return `{"ok":true,"result":[{"update_id":10,"message":{"message_id":1,"date":0,` +
		`"chat":{"id":555,"type":"private"},"text":` + string(msg) + `}}]}`
}

// seamUpdates 让 getUpdates 只在第一次返回一批更新，之后返回空数组，
// 免得假服务被无限轮询刷屏。
func seamUpdates(batch string) func(method string, call int) string {
	return func(method string, call int) string {
		switch method {
		case "getMe":
			return seamGetMe
		case "getUpdates":
			if call == 1 {
				return batch
			}
			return `{"ok":true,"result":[]}`
		}
		return ""
	}
}

// RunBot 把命中消息交给 handler，并把 handler 的回复发回原会话。
func TestRunBot_Replies(t *testing.T) {
	const incoming = "https://guangyapan.com/s/abc_x 提取码 nujo"
	srv, log := seamTelegram(t, seamUpdates(seamUpdateBatch(t, incoming)))
	n, err := NewAtServerURL(seamConfig(""), nil, nil, srv.URL)
	if err != nil {
		t.Fatalf("NewAtServerURL: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.RunBot(ctx, func(text string) string { return "已入库：" + text })

	select {
	case got := <-log.Sent:
		if got != "已入库："+incoming {
			t.Errorf("回复内容 = %q, 期望回显入站文本", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("RunBot 未在超时内回复，调用记录 %v", log.methods())
	}
}

// handler 返回空串表示这条消息不归它处理，不应发出任何回复。
func TestRunBot_SkipsEmptyReply(t *testing.T) {
	srv, log := seamTelegram(t, seamUpdates(seamUpdateBatch(t, "你好")))
	n, err := NewAtServerURL(seamConfig(""), nil, nil, srv.URL)
	if err != nil {
		t.Fatalf("NewAtServerURL: %v", err)
	}

	seen := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.RunBot(ctx, func(text string) string {
		seen <- text
		return ""
	})

	select {
	case got := <-seen:
		if got != "你好" {
			t.Errorf("handler 收到 %q, 期望 \"你好\"", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("RunBot 未在超时内投递更新，调用记录 %v", log.methods())
	}

	// 回复是 handler 之后的同步动作，给一次轮询的余量再断言没有发出
	time.Sleep(200 * time.Millisecond)
	for _, m := range log.methods() {
		if m == "sendMessage" {
			t.Error("空回复不应触发 sendMessage")
			return
		}
	}
}
