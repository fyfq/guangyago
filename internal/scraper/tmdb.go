package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/netutil"
	"github.com/odin/guangya-go/internal/ratelimit"
)

const baseURL = "https://api.themoviedb.org/3"

// Client 是 TMDB v3 的薄封装。
type Client struct {
	apiKey     string
	bearer     bool
	language   string
	fallback   string
	imageBase  string
	posterSize string
	posterDir  string
	cache      bool
	http       *http.Client
	// limiter 把请求频率压在 TMDB 上层保护阈值（约 40 次/秒）之下。
	limiter *ratelimit.Limiter
}

// IsBearer 判断密钥是否为 TMDB 的 Read Access Token（JWT），
// 这类密钥要走 Authorization 头，而不是 api_key 查询参数。
func IsBearer(key string) bool {
	return strings.HasPrefix(key, "eyJ") || len(key) > 64
}

// New 构造客户端；API Key 为空时返回 (nil, nil)，调用方需据此提示未配置。
// proxy 为空指针或零值时直连；rate 为 nil 时使用官方建议的默认限流值。
func New(cfg config.TMDBConfig, posterDir string, proxy *config.ProxyConfig, rate *config.TMDBRate) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, nil
	}
	base := cfg.ImageBase
	if base == "" {
		base = "https://image.tmdb.org/t/p"
	}
	size := cfg.PosterSize
	if size == "" {
		size = "w500"
	}
	lang := cfg.Language
	if lang == "" {
		lang = "zh-CN"
	}
	hc, err := httpClient(proxy)
	if err != nil {
		return nil, err
	}
	return &Client{
		apiKey:     cfg.APIKey,
		bearer:     IsBearer(cfg.APIKey),
		language:   lang,
		fallback:   "en-US",
		imageBase:  base,
		posterSize: size,
		posterDir:  posterDir,
		cache:      cfg.CachePoster,
		http:       hc,
		limiter:    newLimiter(rate),
	}, nil
}

// newLimiter 按配置构造 TMDB 限流器；rate 为空时使用官方建议默认值。
func newLimiter(rate *config.TMDBRate) *ratelimit.Limiter {
	if rate == nil {
		d := config.Default().Rate.TMDB
		rate = &d
	}
	return ratelimit.New(rate.EffectivePerSecond(), rate.Burst, rate.Enable)
}

// TestConnection 验证 TMDB API Key 是否可用，用于设置页的连通性测试。
func TestConnection(ctx context.Context, apiKey string, proxy *config.ProxyConfig) netutil.TestResult {
	start := time.Now()
	if strings.TrimSpace(apiKey) == "" {
		return netutil.TestResult{Message: "还没有填写 TMDB API Key"}
	}
	hc, err := httpClient(proxy)
	if err != nil {
		return netutil.TestResult{Message: "代理配置有误", Detail: err.Error()}
	}
	params := url.Values{}
	u := baseURL + "/configuration"
	if !IsBearer(apiKey) {
		params.Set("api_key", apiKey)
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return netutil.TestResult{Message: "构造请求失败", Detail: err.Error()}
	}
	if IsBearer(apiKey) {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return netutil.TestResult{Message: "连接 TMDB 失败，检查网络或代理", Detail: err.Error(), Elapsed: since(start)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return netutil.TestResult{Message: "API Key 无效（TMDB 返回 " + strconv.Itoa(resp.StatusCode) + "）", Elapsed: since(start)}
	}
	if resp.StatusCode != http.StatusOK {
		return netutil.TestResult{Message: "TMDB 返回 " + strconv.Itoa(resp.StatusCode), Detail: strings.TrimSpace(string(body)), Elapsed: since(start)}
	}
	var cfgResp struct {
		Images struct {
			SecureBaseURL string `json:"secure_base_url"`
		} `json:"images"`
	}
	detail := ""
	if err := json.Unmarshal(body, &cfgResp); err == nil && cfgResp.Images.SecureBaseURL != "" {
		detail = "图片服务：" + cfgResp.Images.SecureBaseURL
	}
	return netutil.TestResult{OK: true, Message: "TMDB 连接正常", Detail: detail, Elapsed: since(start)}
}

func since(start time.Time) int64 { return time.Since(start).Milliseconds() }

func httpClient(proxy *config.ProxyConfig) (*http.Client, error) {
	if proxy == nil {
		return &http.Client{Timeout: 20 * time.Second}, nil
	}
	return netutil.HTTPClient(proxy.Addr, proxy.Username, proxy.Password, proxy.Enable, 20*time.Second)
}

// Result 是一次刮削的结果。
type Result struct {
	MediaType      string
	TMDBID         int64
	Title          string
	OriginalTitle  string
	Year           int
	Overview       string
	Genres         string
	VoteAverage    float64
	Runtime        int
	PosterURL      string
	PosterPath     string
	MatchScore     float64 // 与原始条目的匹配分，0~1
	LowConfidence  bool    // 标题与年份都对不上，建议人工确认
	AlternateFound bool    // 另一侧类型也存在同 ID 条目
}

// MatchRef 是原始条目提供的参考信息，用于在电影/剧集之间做选择与置信度判断。
// Hint 为已知类型（movie / tv），空表示未知，由代码自行试探。
type MatchRef struct {
	Title    string // 清洗后的标题
	RawTitle string // 分享原文里的文件名
	Year     int
	Hint     string
}

// goodEnoughScore 达到此分数即认定命中，不再试探另一种类型。
const goodEnoughScore = 0.75

// lowConfidenceScore 低于此分视为低置信度：标题与年份都对不上，交给人工确认。
const lowConfidenceScore = 0.3

// refTitle 返回用于比对的最佳参考标题，优先清洗后的标题。
func (r MatchRef) refTitle() string {
	if strings.TrimSpace(r.Title) != "" {
		return r.Title
	}
	return r.RawTitle
}

// 剧集线索：文件名里带季、集、剧集等标记时，优先按剧集匹配。
var (
	epMarkRe     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s\d{1,2}[ ._-]?e\d{1,3}(?:[^a-z0-9]|$)`)
	seasonRe     = regexp.MustCompile(`(?i)(第\s*[0-9零一二三四五六七八九十两]+\s*季|season\s*\d{1,2}|series\s*\d{1,2})`)
	episodeRe    = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:ep|eps|episode)\s*\d{1,3}(?:[^a-z0-9]|$)`)
	collectRe    = regexp.MustCompile(`(全\s*\d{1,3}\s*集|\d{1,3}\s*集全|全集|剧集|连续剧|电视剧|番剧|国漫|美剧|韩剧|日剧|英剧|港剧|台剧|泰剧|综艺|纪录(?:片|剧))`)
	seriesWordRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:the\s+)?(?:tv\s*)?series(?:[^a-z0-9]|$)`)
	movieHintRe  = regexp.MustCompile(`(剧场版|动画电影|院线|电影版)|(?i)(?:^|[^a-z0-9])(?:movie|film)(?:[^a-z0-9]|$)`)
	// 仅季标记（如 "S03"）同样指向剧集；更新至/完结/季播等运营词也是剧集线索
	seasonOnlyRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s\d{1,2}(?:[^a-z0-9]|$)`)
	tvWordRe     = regexp.MustCompile(`(更新至|已完结|连载中|季播|周更|日更)`)
)

// GuessMediaType 从标题文本推断影视类型，返回 movie / tv，无法判断时返回空串。
// 多个文本都可传入（标题、原始文件名），任一命中剧集线索即判为剧集。
func GuessMediaType(texts ...string) string {
	tv := false
	for _, s := range texts {
		if s == "" {
			continue
		}
		if epMarkRe.MatchString(s) || seasonRe.MatchString(s) || seasonOnlyRe.MatchString(s) ||
			episodeRe.MatchString(s) || collectRe.MatchString(s) || tvWordRe.MatchString(s) ||
			seriesWordRe.MatchString(s) {
			tv = true
		}
	}
	if tv {
		return "tv"
	}
	for _, s := range texts {
		if s != "" && movieHintRe.MatchString(s) {
			return "movie"
		}
	}
	return ""
}

// normTitle 归一化标题：转小写，只保留字母、数字与 CJK 字符，便于跨语言比对。
func normTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 0x4e00 && r <= 0x9fff, // CJK 统一表意文字
			r >= 0x3040 && r <= 0x30ff, // 日文假名
			r >= 0xac00 && r <= 0xd7af, // 韩文音节
			r >= 0x0400 && r <= 0x04ff: // 西里尔字母
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bigramJaccard 用二元组集合的 Jaccard 相似度比较两个字符串，返回 0~1。
// 对中英文混排、顺序基本一致的片名足够稳健。
func bigramJaccard(a, b string) float64 {
	a, b = normTitle(a), normTitle(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return 0.9
	}
	sa, sb := bigrams(a), bigrams(b)
	if len(sa) == 0 || len(sb) == 0 {
		if strings.ContainsRune(a, []rune(b)[0]) {
			return 0.3
		}
		return 0
	}
	inter := 0
	for k := range sa {
		if sb[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(sa)+len(sb)-inter)
}

func bigrams(s string) map[string]bool {
	m := make(map[string]bool, len(s))
	runes := []rune(s)
	if len(runes) < 2 {
		if len(runes) == 1 {
			m[string(runes[0])+string(runes[0])] = true
		}
		return m
	}
	for i := 0; i+1 < len(runes); i++ {
		m[string(runes[i])+string(runes[i+1])] = true
	}
	return m
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// stripEpisodeMarkers 去掉季/集与「全X集」等标记，得到用于搜索的纯片名。
// 「权力的游戏 第三季」「Game.of.Thrones.S03」都归一为纯片名，搜索命中率更高。
func stripEpisodeMarkers(s string) string {
	s = epMarkRe.ReplaceAllString(s, " ")
	s = seasonRe.ReplaceAllString(s, " ")
	s = seasonOnlyRe.ReplaceAllString(s, " ")
	s = episodeRe.ReplaceAllString(s, " ")
	s = collectRe.ReplaceAllString(s, " ")
	s = tvWordRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// uniqueStrings 去重去空，保持原有顺序。
func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// yearScore 年份吻合度：同年 1 分，差 1 年 0.8，差 2 年 0.4，再远 0；缺年份时给中性 0.5。
func yearScore(cand, ref int) float64 {
	if ref <= 0 || cand <= 0 {
		return 0.5
	}
	d := cand - ref
	if d < 0 {
		d = -d
	}
	switch {
	case d == 0:
		return 1
	case d == 1:
		return 0.8
	case d == 2:
		return 0.4
	default:
		return 0
	}
}

// scoreMatch 综合标题相似度与年份吻合度给出匹配分，0~1。
func scoreMatch(cand *Result, ref MatchRef) float64 {
	want := ref.refTitle()
	titleScore := 0.0
	if want != "" {
		titleScore = bigramJaccard(cand.Title, want)
		if s := bigramJaccard(cand.OriginalTitle, want); s > titleScore {
			titleScore = s
		}
		if s := bigramJaccard(cand.Title, ref.RawTitle); s > titleScore {
			titleScore = s
		}
	} else {
		titleScore = 0.5
	}
	return 0.7*titleScore + 0.3*yearScore(cand.Year, ref.Year)
}

// maxAttempts 是单次请求的最大尝试次数；遇到 429 会按 Retry-After 退让后重试。
const maxAttempts = 3

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	u := baseURL + path
	if !c.bearer {
		params.Set("api_key", c.apiKey)
	}
	url := u + "?" + params.Encode()

	for attempt := 1; ; attempt++ {
		// 每次调用 API 前先取令牌，把频率压在官方阈值之下
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		if c.bearer {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusOK {
			return json.Unmarshal(body, out)
		}
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("not found")
		}
		// 官方要求：收到 429 就应当退让，按 Retry-After 等待后重试
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts {
			if wait := retryAfter(resp.Header.Get("Retry-After")); wait > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
		}
		return fmt.Errorf("TMDB 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// retryAfter 解析 Retry-After（秒数），解析不出时给一个 1 秒的默认退让。
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Second
	}
	if sec, err := strconv.Atoi(v); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}
	return time.Second
}

type detail struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	Overview      string  `json:"overview"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	VoteAverage   float64 `json:"vote_average"`
	Runtime       int     `json:"runtime"`
	PosterPath    string  `json:"poster_path"`
	Genres        []struct {
		Name string `json:"name"`
	} `json:"genres"`
	EpisodeRunTime []int `json:"episode_run_time"`
}

func (d *detail) toResult(mediaType string, c *Client) *Result {
	title := d.Title
	original := d.OriginalTitle
	date := d.ReleaseDate
	if mediaType == "tv" {
		title = d.Name
		original = d.OriginalName
		date = d.FirstAirDate
	}
	runtime := d.Runtime
	if runtime == 0 && len(d.EpisodeRunTime) > 0 {
		runtime = d.EpisodeRunTime[0]
	}
	genres := make([]string, 0, len(d.Genres))
	for _, g := range d.Genres {
		genres = append(genres, g.Name)
	}
	r := &Result{
		MediaType:     mediaType,
		TMDBID:        d.ID,
		Title:         title,
		OriginalTitle: original,
		Year:          yearOf(date),
		Overview:      d.Overview,
		Genres:        strings.Join(genres, " / "),
		VoteAverage:   d.VoteAverage,
		Runtime:       runtime,
	}
	if d.PosterPath != "" {
		r.PosterURL = c.imageBase + "/" + c.posterSize + d.PosterPath
	}
	return r
}

// Candidate 是一次查询得到的 TMDB 条目预览，用于「补 ID 先确认再刮削」。
// 只做查询，不写任何库表。
type Candidate struct {
	MediaType     string  `json:"media_type"`
	TMDBID        int64   `json:"tmdb_id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          int     `json:"year"`
	Overview      string  `json:"overview"`
	Genres        string  `json:"genres"`
	VoteAverage   float64 `json:"vote_average"`
	Runtime       int     `json:"runtime"`
	PosterURL     string  `json:"poster_url"`
	SiteURL       string  `json:"site_url"`
	MediaLabel    string  `json:"media_label"`
}

// MediaLabel 把 media_type 翻译成中文展示文案。
func MediaLabel(mediaType string) string {
	if mediaType == "tv" {
		return "剧集"
	}
	return "电影"
}

// siteURL 拼出 TMDB 站点地址，便于人工核对。
func siteURL(mediaType string, id int64) string {
	kind := "movie"
	if mediaType == "tv" {
		kind = "tv"
	}
	return "https://www.themoviedb.org/" + kind + "/" + strconv.FormatInt(id, 10)
}

// toCandidate 把详情转成预览结构，简介按字符截断，避免接口返回过长。
func (d *detail) toCandidate(mediaType string, c *Client) *Candidate {
	r := d.toResult(mediaType, c)
	ov := r.Overview
	if utf8.RuneCountInString(ov) > 160 {
		ov = string([]rune(ov)[:159]) + "…"
	}
	return &Candidate{
		MediaType:     r.MediaType,
		TMDBID:        r.TMDBID,
		Title:         r.Title,
		OriginalTitle: r.OriginalTitle,
		Year:          r.Year,
		Overview:      ov,
		Genres:        r.Genres,
		VoteAverage:   r.VoteAverage,
		Runtime:       r.Runtime,
		PosterURL:     r.PosterURL,
		SiteURL:       siteURL(r.MediaType, r.TMDBID),
		MediaLabel:    MediaLabel(r.MediaType),
	}
}

// LookupByID 按 TMDB ID 精确查询条目，用于补 ID 时先核对。
// mediaType 为空时依次尝试电影与剧集；都查不到时第二个返回值说明原因。
func (c *Client) LookupByID(ctx context.Context, tmdbID int64, mediaType string) (*Candidate, error) {
	if c == nil {
		return nil, fmt.Errorf("未配置 TMDB API Key")
	}
	order := []string{"movie", "tv"}
	switch mediaType {
	case "tv":
		order = []string{"tv"}
	case "movie":
		order = []string{"movie"}
	}
	var lastErr error
	for _, mt := range order {
		// 首选配置语言，中文缺失时回退英文再试一次
		for _, lang := range []string{c.language, c.fallback} {
			var d detail
			err := c.get(ctx, fmt.Sprintf("/%s/%d", mt, tmdbID), url.Values{"language": {lang}}, &d)
			if err == nil {
				return d.toCandidate(mt, c), nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("未找到条目")
	}
	return nil, fmt.Errorf("TMDB %d 查询不到（%v）", tmdbID, lastErr)
}

// LookupByTitle 按片名搜索候选，返回最多 limit 条，供人工确认后选定。
func (c *Client) LookupByTitle(ctx context.Context, title string, year int, limit int) ([]Candidate, error) {
	if c == nil {
		return nil, fmt.Errorf("未配置 TMDB API Key")
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("标题为空，无法搜索")
	}
	if limit <= 0 {
		limit = 8
	}
	params := url.Values{"query": {title}, "language": {c.language}, "include_adult": {"false"}}
	var sr searchResp
	if err := c.get(ctx, "/search/multi", params, &sr); err != nil {
		return nil, err
	}
	items := make([]searchItem, 0, len(sr.Results))
	for _, r := range sr.Results {
		if r.MediaType == "movie" || r.MediaType == "tv" {
			items = append(items, r)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("未搜索到：%s", title)
	}
	// 标题相似度 + 年份综合排序，让最可能命中的排前面，而不只是年份完全命中
	sort.SliceStable(items, func(i, j int) bool {
		return scoreCandidate(items[i], i, title, "", year, "") > scoreCandidate(items[j], j, title, "", year, "")
	})
	out := make([]Candidate, 0, limit)
	for _, r := range items {
		if len(out) >= limit {
			break
		}
		// 搜索结果只有摘要信息，取详情补全海报与简介
		full, err := c.LookupByID(ctx, r.ID, r.MediaType)
		if err != nil {
			full = &Candidate{
				MediaType:  r.MediaType,
				TMDBID:     r.ID,
				Title:      r.displayTitle(),
				Year:       yearOf(r.date()),
				SiteURL:    siteURL(r.MediaType, r.ID),
				MediaLabel: MediaLabel(r.MediaType),
			}
		}
		out = append(out, *full)
	}
	return out, nil
}

// Fetch 按 TMDB ID 取详情。
//
// TMDB 的电影与剧集是两套独立 ID 空间，同一个数字 ID 两侧可能都存在不同条目，
// 所以这里不再无脑「先电影后剧集」：
//  1. ref.Hint 明确指定类型时只查该类型；
//  2. 否则按标题里的季/集线索决定试探顺序，标题无参考信息时回退为电影优先；
//  3. 两种类型都取到时，用「标题相似度 + 年份吻合度」打分选更贴合的那个；
//  4. 标题与年份都对不上时标记 LowConfidence，由调用方转人工确认。
func (c *Client) Fetch(ctx context.Context, tmdbID int64, ref MatchRef) (*Result, error) {
	var order []string
	switch strings.ToLower(strings.TrimSpace(ref.Hint)) {
	case "movie":
		order = []string{"movie"}
	case "tv":
		order = []string{"tv"}
	default:
		if GuessMediaType(ref.Title, ref.RawTitle) == "tv" {
			order = []string{"tv", "movie"}
		} else {
			order = []string{"movie", "tv"}
		}
	}
	langs := []string{c.language}
	if c.fallback != "" && c.fallback != c.language {
		langs = append(langs, c.fallback)
	}

	var best *Result
	var bestScore float64
	tried := 0
	tryType := func(mt string) {
		d, ok := c.fetchDetail(ctx, mt, tmdbID, langs)
		if !ok {
			return
		}
		tried++
		r := d.toResult(mt, c)
		if s := scoreMatch(r, ref); best == nil || s > bestScore {
			best, bestScore = r, s
		}
	}
	for _, mt := range order {
		tryType(mt)
		// 命中度足够，或没有参考信息可比，就不再打扰另一侧
		if best != nil && (ref.refTitle() == "" || bestScore >= goodEnoughScore) {
			break
		}
	}
	// hint 只查了单侧但命中度很低：多半是类型记录错了，补试另一侧再比
	if best != nil && tried == 1 && bestScore < lowConfidenceScore {
		other := "movie"
		if order[0] == "movie" {
			other = "tv"
		}
		tryType(other)
	}
	if best == nil {
		return nil, fmt.Errorf("TMDB %d 未找到对应条目", tmdbID)
	}
	best.MatchScore = bestScore
	best.AlternateFound = tried > 1
	// 标题与年份都对不上，说明很可能刮错了，交给人工确认
	best.LowConfidence = ref.refTitle() != "" && bestScore < lowConfidenceScore
	return best, nil
}

// fetchDetail 依次用配置语言与回退语言取详情，取到即返回。
func (c *Client) fetchDetail(ctx context.Context, mt string, tmdbID int64, langs []string) (*detail, bool) {
	var d detail
	for _, lang := range langs {
		if err := c.get(ctx, fmt.Sprintf("/%s/%d", mt, tmdbID), url.Values{"language": {lang}}, &d); err == nil {
			return &d, true
		}
	}
	return nil, false
}

type searchItem struct {
	ID            int64  `json:"id"`
	MediaType     string `json:"media_type"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	Name          string `json:"name"`
	OriginalName  string `json:"original_name"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
}

type searchResp struct {
	Results []searchItem `json:"results"`
}

// displayTitle 返回候选的展示标题，剧集用 name。
func (it searchItem) displayTitle() string {
	if it.MediaType == "tv" {
		return it.Name
	}
	return it.Title
}

// original 返回候选的原始语言标题，比对跨语言片名时更可靠。
func (it searchItem) original() string {
	if it.MediaType == "tv" {
		return it.OriginalName
	}
	return it.OriginalTitle
}

// date 返回候选的上映/首播日期，剧集用首播日。
func (it searchItem) date() string {
	if it.MediaType == "tv" {
		return it.FirstAirDate
	}
	return it.ReleaseDate
}

// searchMulti 调用 /search/multi，只保留电影与剧集条目。
func (c *Client) searchMulti(ctx context.Context, query string) []searchItem {
	params := url.Values{"query": {query}, "language": {c.language}, "include_adult": {"false"}}
	var sr searchResp
	if err := c.get(ctx, "/search/multi", params, &sr); err != nil {
		return nil
	}
	out := make([]searchItem, 0, len(sr.Results))
	for _, r := range sr.Results {
		if r.MediaType == "movie" || r.MediaType == "tv" {
			out = append(out, r)
		}
	}
	return out
}

// searchKind 按类型定向搜索；电影/剧集分别支持 year / first_air_date_year 过滤，
// multi 搜不到或结果很弱时用它兜底，年份能显著提高命中。
func (c *Client) searchKind(ctx context.Context, mt, query string, year int) []searchItem {
	params := url.Values{"query": {query}, "language": {c.language}, "include_adult": {"false"}}
	if year > 0 {
		if mt == "tv" {
			params.Set("first_air_date_year", strconv.Itoa(year))
		} else {
			params.Set("year", strconv.Itoa(year))
		}
	}
	var sr searchResp
	if err := c.get(ctx, "/search/"+mt, params, &sr); err != nil {
		return nil
	}
	out := make([]searchItem, 0, len(sr.Results))
	for _, r := range sr.Results {
		r.MediaType = mt
		out = append(out, r)
	}
	return out
}

// scoreCandidate 给搜索候选打分：标题相似度为主，年份与类型线索为辅，
// 外加一点搜索排名权重（TMDB 排名靠前的通常更可靠）。0~1.15。
func scoreCandidate(it searchItem, idx int, refTitle, rawTitle string, year int, guess string) float64 {
	titleScore := 0.5
	if refTitle != "" {
		titleScore = maxF(bigramJaccard(it.displayTitle(), refTitle), bigramJaccard(it.original(), refTitle))
		if rawTitle != "" {
			titleScore = maxF(titleScore, bigramJaccard(it.displayTitle(), rawTitle))
		}
	}
	s := 0.7*titleScore + 0.3*yearScore(yearOf(it.date()), year)
	if guess != "" && it.MediaType == guess {
		s += 0.1
	}
	s += 0.05 / float64(idx+1)
	return s
}

// Search 按标题搜索，综合标题相似度、年份与类型线索挑选最佳候选。
// 季/集标记会拖累搜索命中率，优先用去掉标记的纯片名查询；
// 清洗标题搜不到时退回原始文件名，multi 无果或结果很弱时再按类型带年份定向搜索。
func (c *Client) Search(ctx context.Context, title, rawTitle string, year int) (*Result, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title, rawTitle = strings.TrimSpace(rawTitle), ""
	}
	if title == "" {
		return nil, fmt.Errorf("标题为空，无法搜索")
	}
	guess := GuessMediaType(title, rawTitle)
	queries := uniqueStrings([]string{
		stripEpisodeMarkers(title),
		title,
		stripEpisodeMarkers(rawTitle),
		rawTitle,
	})

	var best searchItem
	bestScore := -1.0
	consider := func(items []searchItem) {
		for i, it := range items {
			if s := scoreCandidate(it, i, title, rawTitle, year, guess); s > bestScore {
				best, bestScore = it, s
			}
		}
	}
	for _, q := range queries {
		consider(c.searchMulti(ctx, q))
		if bestScore >= goodEnoughScore {
			break
		}
	}
	// 没有结果或分数很低时，按类型定向搜索并带上年份兜底
	if bestScore < lowConfidenceScore {
		q := queries[0]
		consider(c.searchKind(ctx, "movie", q, year))
		consider(c.searchKind(ctx, "tv", q, year))
	}
	if bestScore < 0 {
		return nil, fmt.Errorf("搜索结果中没有影视条目：%s", title)
	}
	// 搜索已确定类型，取详情时必须带上，否则又会被按电影优先重判
	return c.Fetch(ctx, best.ID, MatchRef{Title: title, RawTitle: rawTitle, Year: year, Hint: best.MediaType})
}

// SavePoster 下载海报到本地，返回相对路径。海报为空或下载失败时返回空串。
func (c *Client) SavePoster(ctx context.Context, r *Result) string {
	if r == nil || r.PosterURL == "" || !c.cache || c.posterDir == "" {
		return ""
	}
	if err := os.MkdirAll(c.posterDir, 0o755); err != nil {
		return ""
	}
	name := fmt.Sprintf("%s-%d.jpg", r.MediaType, r.TMDBID)
	dst := filepath.Join(c.posterDir, name)
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	// 海报走的是 image.tmdb.org，同样纳入 TMDB 限流，避免批量刮削时打满
	if err := c.limiter.Wait(ctx); err != nil {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.PosterURL, nil)
	if err != nil {
		return ""
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	f, err := os.Create(dst)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return ""
	}
	return dst
}

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
