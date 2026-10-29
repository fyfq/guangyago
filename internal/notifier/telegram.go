package notifier

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
	"github.com/odin/guangya-go/internal/netutil"
	"github.com/odin/guangya-go/internal/ratelimit"
)

// Notifier 负责把资源卡片推送到 Telegram 频道，并运行 Bot 接收录入消息。
type Notifier struct {
	bot    *bot.Bot
	chatID any
	silent bool
	// limiter 控制发送节奏：每一次 API 调用（含海报失败后的文本回退）都要取令牌，
	// 否则一次推送可能连发两条，直接吃掉两倍的配额。
	limiter *ratelimit.Limiter
}

// New 构造推送器；Token 为空时返回 (nil, nil)。
// rate 为 nil 时使用官方建议的默认间隔。
func New(cfg config.TelegramConfig, proxy *config.ProxyConfig, rate *config.TelegramRate) (*Notifier, error) {
	return NewAtServerURL(cfg, proxy, rate, "")
}

// NewAtServerURL 与 New 相同，只把 Telegram API 根地址换成 serverURL
//（空值走官方地址）。测试接缝：离线单测用它接一个假 Bot API，不走外网。
func NewAtServerURL(cfg config.TelegramConfig, proxy *config.ProxyConfig, rate *config.TelegramRate, serverURL string) (*Notifier, error) {
	if cfg.Token == "" {
		return nil, nil
	}
	b, err := newBot(cfg, proxy, serverURL)
	if err != nil {
		return nil, err
	}
	tgNotifier := &Notifier{bot: b, chatID: cfg.ChatID.Any(), silent: cfg.Silent, limiter: newLimiter(rate)}
	return tgNotifier, nil
}

// newLimiter 按配置构造 Telegram 发送限流器。
// 间隔 interval 换算成「每秒 1000/interval 个令牌、桶容量 1」，
// 等价于两次发送之间至少间隔 interval 毫秒。
func newLimiter(rate *config.TelegramRate) *ratelimit.Limiter {
	if rate == nil {
		d := config.Default().Rate.Telegram
		rate = &d
	}
	ms := rate.EffectiveIntervalMS()
	if ms <= 0 {
		ms = config.DefaultTelegramIntervalMS
	}
	return ratelimit.New(float64(time.Second)/float64(time.Duration(ms)*time.Millisecond), 1, rate.Enable)
}

// newBot 按代理配置构造 Bot，未启用代理时保持库的默认客户端。
// serverURL 非空时把 Bot API 指向该地址（测试用假服务），其余行为不变。
func newBot(cfg config.TelegramConfig, proxy *config.ProxyConfig, serverURL string) (*bot.Bot, error) {
	var opts []bot.Option
	if serverURL != "" {
		opts = append(opts, bot.WithServerURL(serverURL))
	}
	if proxy == nil || !proxy.Enable {
		return bot.New(cfg.Token, opts...)
	}
	hc, err := netutil.HTTPClient(proxy.Addr, proxy.Username, proxy.Password, true, 40*time.Second)
	if err != nil {
		return nil, err
	}
	opts = append(opts, bot.WithHTTPClient(10*time.Second, hc))
	return bot.New(cfg.Token, opts...)
}

// TestTelegram 验证 Bot Token 与频道 ID：getMe → getChat → Bot 在频道内的身份。
func TestTelegram(ctx context.Context, cfg config.TelegramConfig, proxy *config.ProxyConfig) netutil.TestResult {
	return testTelegramAt(ctx, cfg, proxy, "")
}

// testTelegramAt 是 TestTelegram 的可注入 Bot API 地址版本，测试里指向假服务。
func testTelegramAt(ctx context.Context, cfg config.TelegramConfig, proxy *config.ProxyConfig, serverURL string) netutil.TestResult {
	start := time.Now()
	if strings.TrimSpace(cfg.Token) == "" {
		return netutil.TestResult{Message: "还没有填写 Bot Token"}
	}
	b, err := newBot(cfg, proxy, serverURL)
	if err != nil {
		return netutil.TestResult{Message: "Bot 初始化失败", Detail: err.Error()}
	}
	me, err := b.GetMe(ctx)
	if err != nil {
		return netutil.TestResult{
			Message: "Bot Token 无效，或无法连接 Telegram（检查网络与代理）",
			Detail:  err.Error(),
			Elapsed: since(start),
		}
	}
	msg := "Bot @" + me.Username + " 连接正常"
	detail := ""
	if chatID := cfg.ChatID.Any(); chatID != nil {
		chat, err := b.GetChat(ctx, &bot.GetChatParams{ChatID: chatID})
		if err != nil {
			return netutil.TestResult{
				Message: "Bot 正常，但读取频道失败（ID/用户名有误，或 Bot 不在频道内）",
				Detail:  err.Error(),
				Elapsed: since(start),
			}
		}
		msg += " · 频道「" + chat.Title + "」"
		if mem, err := b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chatID, UserID: me.ID}); err == nil {
			role := string(mem.Type)
			detail = "Bot 在该频道的身份：" + role
			if role != "administrator" && role != "owner" && role != "creator" {
				return netutil.TestResult{
					Message: msg + " · 但 Bot 不是管理员，无法发消息",
					Detail:  detail,
					Elapsed: since(start),
				}
			}
		}
	} else {
		detail = "未填频道 ID，本次只验证了 Token"
	}
	return netutil.TestResult{OK: true, Message: msg, Detail: detail, Elapsed: since(start)}
}

func since(start time.Time) int64 { return time.Since(start).Milliseconds() }

// Send 推送单条资源卡片，返回消息 ID。
func (n *Notifier) Send(ctx context.Context, l *model.Link) (int64, error) {
	if n == nil || n.bot == nil {
		return 0, fmt.Errorf("未配置 Telegram Bot Token")
	}
	if n.chatID == nil {
		return 0, fmt.Errorf("未配置频道 ID")
	}
	caption := Caption(l)
	// 主发送（海报）先取一次令牌
	if err := n.limiter.Wait(ctx); err != nil {
		return 0, err
	}
	params := &bot.SendPhotoParams{
		ChatID:              n.chatID,
		Caption:             caption,
		ParseMode:           models.ParseModeHTML,
		DisableNotification: n.silent,
	}
	if l.PosterURL != "" {
		params.Photo = &models.InputFileString{Data: l.PosterURL}
	}
	msg, err := n.bot.SendPhoto(ctx, params)
	if err != nil && l.PosterURL != "" {
		// 海报不可用时退化为纯文本；回退也是一次真实发送，同样要过限流
		if werr := n.limiter.Wait(ctx); werr != nil {
			return 0, werr
		}
		msg, err = n.bot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:              n.chatID,
			Text:                caption,
			ParseMode:           models.ParseModeHTML,
			DisableNotification: n.silent,
		})
	}
	if err != nil {
		return 0, err
	}
	return int64(msg.ID), nil
}

// captionLimit 是 Telegram sendPhoto 文案上限，留出余量。
const captionLimit = 1000

// overviewLimit 是简介的展示长度上限，超出部分截断并补省略号。
const overviewLimit = 200

// linkLabel 是分享链接在卡片里的显示文案，会被替换为 HTML 超链接。
const linkLabel = "光鸭资源链接"

// Caption 生成频道卡片文案。每一项各占一行，且**项与项之间都空一行**：
//
//	漫游世界建筑群
//
//	2008
//
//	主持人丹·克鲁克山克周游世界，遍访各式建筑……
//
//	光鸭资源链接
//
//	提取码：nujo
//
//	TMDB: https://www.themoviedb.org/tv/20851
func Caption(l *model.Link) string {
	name := l.Title
	if name == "" {
		name = l.RawTitle
	}

	// 标题区：片名、年份各自成块
	head := []string{"<b>" + html.EscapeString(name) + "</b>"}
	if l.Year > 0 {
		head = append(head, strconv.Itoa(l.Year))
	}

	// 链接区：链接 / 提取码 / TMDB，同样各自成块
	tail := []string{linkLabel}
	if l.Code != "" {
		tail = append(tail, "提取码："+html.EscapeString(l.Code))
	}
	if l.TMDBID > 0 {
		tail = append(tail, "TMDB: https://www.themoviedb.org/"+tmdbPath(string(l.MediaType))+"/"+strconv.FormatInt(l.TMDBID, 10))
	}

	// 预算：总长上限 - 其它块的字符数 - 每两块之间的空行（2 个字符）
	others := make([]string, 0, len(head)+len(tail))
	others = append(others, head...)
	others = append(others, tail...)
	used := 0
	for _, s := range others {
		used += utf8.RuneCountInString(s)
	}
	budget := captionLimit - used - 2*len(others)
	if budget > overviewLimit {
		budget = overviewLimit
	}

	// 拼块：标题区 → 简介 → 链接区，块间统一空一行
	blocks := make([]string, 0, len(others)+1)
	blocks = append(blocks, head...)
	if ov := truncateRunes(oneLine(l.Overview), budget); ov != "" {
		blocks = append(blocks, html.EscapeString(ov))
	}
	blocks = append(blocks, tail...)

	caption := strings.Join(blocks, "\n\n")
	if isHTTPURL(l.URL) {
		caption = strings.Replace(
			caption,
			linkLabel,
			`<a href="`+html.EscapeString(l.URL)+`">`+linkLabel+`</a>`,
			1,
		)
	}
	return strings.TrimSpace(caption)
}

// isHTTPURL 只给 http/https 地址生成链接，避免 javascript: 等协议进入卡片。
func isHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

var overviewSpaceRe = regexp.MustCompile(`\s+`)

// oneLine 把简介压成单行：TMDB 返回的简介里常带 \r\n 或裸 \r，
// 直接输出会在频道里出现断行和空白，统一折叠成空格。
func oneLine(s string) string {
	return strings.TrimSpace(overviewSpaceRe.ReplaceAllString(s, " "))
}

// tmdbPath 返回 TMDB 站点路径片段：tv 或 movie。
func tmdbPath(mediaType string) string {
	if mediaType == "tv" {
		return "tv"
	}
	return "movie"
}

// truncateRunes 按字符数截断，超长时末尾补省略号。
func truncateRunes(s string, limit int) string {
	if limit <= 1 {
		return ""
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= limit {
		return string(runes)
	}
	// 省略号占一个字符
	return string(runes[:limit-1]) + "…"
}

// RunBot 以 long-polling 方式接收消息，把包含光鸭链接的消息交给 handler 入库，
// 并回复 handler 返回的提示文案。
func (n *Notifier) RunBot(ctx context.Context, handler func(text string) string) {
	if n == nil || n.bot == nil {
		return
	}
	n.bot.RegisterHandlerMatchFunc(func(update *models.Update) bool {
		return update.Message != nil && update.Message.Text != ""
	}, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		text := update.Message.Text
		reply := handler(text)
		if reply == "" {
			return
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   reply,
		})
	})
	n.bot.Start(ctx)
}
