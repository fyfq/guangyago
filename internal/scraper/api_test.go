package scraper

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/ratelimit"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeTMDB 返回按 URL path 路由的假 TMDB 客户端。
func fakeTMDB(t *testing.T, route func(path string, q url.Values) (int, string)) *Client {
	t.Helper()
	return &Client{
		apiKey:     "test-key",
		language:   "zh-CN",
		fallback:   "en-US",
		imageBase:  "https://image.tmdb.org/t/p",
		posterSize: "w500",
		posterDir:  t.TempDir(),
		cache:      true,
		http: &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			path := strings.TrimPrefix(r.URL.Path, "/3")
			code, body := route(path, r.URL.Query())
			return &http.Response{
				StatusCode: code,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		})},
		limiter: ratelimit.New(1e6, 1000, true),
	}
}

const movieJSON = `{"id":1,"title":"流浪地球2","original_title":"The Wandering Earth II","release_date":"2023-01-22","overview":"太阳危机","vote_average":8.0,"runtime":173,"poster_path":"/p.jpg","genres":[{"name":"科幻"}]}`
const tvJSON = `{"id":1,"name":"权游","original_name":"Game of Thrones","first_air_date":"2011-04-17","overview":"铁王座","vote_average":9.0,"episode_run_time":[57],"genres":[{"name":"剧情"}]}`

func TestGet_StatusHandling(t *testing.T) {
	out := struct {
		ID int64 `json:"id"`
	}{}
	c := fakeTMDB(t, func(string, url.Values) (int, string) { return 200, `{"id":7}` })
	if err := c.get(context.Background(), "/movie/7", url.Values{}, &out); err != nil {
		t.Fatalf("200 应成功: %v", err)
	}
	if out.ID != 7 {
		t.Errorf("响应未解析: %+v", out)
	}

	c = fakeTMDB(t, func(string, url.Values) (int, string) { return 404, `` })
	if err := c.get(context.Background(), "/movie/9", url.Values{}, &out); err == nil || err.Error() != "not found" {
		t.Errorf("404 应返回 not found，实际 %v", err)
	}

	c = fakeTMDB(t, func(string, url.Values) (int, string) { return 500, `boom` })
	if err := c.get(context.Background(), "/movie/9", url.Values{}, &out); err == nil || !strings.Contains(err.Error(), "TMDB 返回 500") {
		t.Errorf("500 应报错并带状态码，实际 %v", err)
	}

	// 429 + Retry-After：ctx 在等待期间取消应返回 ctx 错误，而不是死等
	c = fakeTMDB(t, func(string, url.Values) (int, string) {
		return 429, `rate limited`
	})
	c.http.Transport = rtFunc(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("Retry-After", "5")
		return &http.Response{StatusCode: 429, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.get(ctx, "/movie/9", url.Values{}, &out); err == nil {
		t.Error("ctx 超时后 429 重试应返回错误")
	}
}

func TestGet_BearerHeader(t *testing.T) {
	var gotAuth string
	c := fakeTMDB(t, func(string, url.Values) (int, string) { return 200, `{"id":1}` })
	c.bearer = true
	c.apiKey = "jwt-token"
	c.http.Transport = rtFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		if _, ok := r.URL.Query()["api_key"]; ok {
			t.Error("Bearer 模式不应带 api_key 查询参数")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":1}`)), Request: r}, nil
	})
	var out struct{}
	if err := c.get(context.Background(), "/movie/1", url.Values{}, &out); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer jwt-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestFetch(t *testing.T) {
	route := func(path string, _ url.Values) (int, string) {
		switch path {
		case "/movie/1":
			return 200, movieJSON
		case "/tv/1":
			return 200, tvJSON
		}
		return 404, ``
	}

	// hint=movie 直接命中，不试探另一侧
	c := fakeTMDB(t, route)
	r, err := c.Fetch(context.Background(), 1, MatchRef{Title: "流浪地球2", Year: 2023, Hint: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if r.MediaType != "movie" || r.Title != "流浪地球2" || r.Year != 2023 || r.Runtime != 173 {
		t.Errorf("movie 详情解析异常: %+v", r)
	}
	if r.AlternateFound || r.LowConfidence {
		t.Errorf("精确命中不应标记 alternate/lowConfidence: %+v", r)
	}
	if r.PosterURL != "https://image.tmdb.org/t/p/w500/p.jpg" {
		t.Errorf("海报地址错误: %q", r.PosterURL)
	}

	// 无 hint 但标题带剧集线索，应优先命中 tv
	r, err = c.Fetch(context.Background(), 1, MatchRef{Title: "权游 第三季"})
	if err != nil {
		t.Fatal(err)
	}
	if r.MediaType != "tv" || r.Title != "权游" || r.Runtime != 57 {
		t.Errorf("tv 详情解析异常: %+v", r)
	}

	// 标题完全对不上：低置信度
	r, err = c.Fetch(context.Background(), 1, MatchRef{Title: "风马牛不相及", Year: 1990, Hint: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.LowConfidence {
		t.Errorf("标题年份都不符时应标记 LowConfidence: %+v", r)
	}

	// 都查不到
	c = fakeTMDB(t, func(string, url.Values) (int, string) { return 404, `` })
	if _, err := c.Fetch(context.Background(), 999, MatchRef{Title: "x"}); err == nil {
		t.Error("TMDB 无此条目应报错")
	}
}

func TestFetch_FallsBackToOtherType(t *testing.T) {
	// hint=movie 命中了低置信度条目，补试 tv 后标题吻合 → 采用 tv
	route := func(path string, _ url.Values) (int, string) {
		switch path {
		case "/movie/2":
			return 200, `{"id":2,"title":"完全无关的片子","release_date":"1900-01-01"}`
		case "/tv/2":
			return 200, tvJSON
		}
		return 404, ``
	}
	c := fakeTMDB(t, route)
	r, err := c.Fetch(context.Background(), 2, MatchRef{Title: "权游", Year: 2011, Hint: "movie"})
	if err != nil {
		t.Fatalf("回退另一侧失败: %v", err)
	}
	if r.MediaType != "tv" {
		t.Errorf("应回退到 tv，实际 %s", r.MediaType)
	}
	if !r.AlternateFound {
		t.Error("尝试过两侧时应标记 AlternateFound")
	}
}

func TestSearch(t *testing.T) {
	multi := `{"results":[{"id":55,"media_type":"movie","title":"流浪地球2","original_title":"The Wandering Earth II","release_date":"2023-01-22"}]}`
	detail := `{"id":55,"title":"流浪地球2","original_title":"The Wandering Earth II","release_date":"2023-01-22","runtime":173,"genres":[]}`

	c := fakeTMDB(t, func(path string, _ url.Values) (int, string) {
		switch path {
		case "/search/multi":
			return 200, multi
		case "/movie/55":
			return 200, detail
		}
		return 404, ``
	})
	r, err := c.Search(context.Background(), "流浪地球2", "", 2023)
	if err != nil {
		t.Fatal(err)
	}
	if r.TMDBID != 55 || r.MediaType != "movie" {
		t.Errorf("搜索结果异常: %+v", r)
	}

	// 空标题
	if _, err := c.Search(context.Background(), "", "", 0); err == nil {
		t.Error("空标题应报错")
	}

	// multi 无结果，走 searchKind 兜底
	c = fakeTMDB(t, func(path string, _ url.Values) (int, string) {
		switch path {
		case "/search/multi":
			return 200, `{"results":[]}`
		case "/search/movie":
			return 200, multi
		case "/movie/55":
			return 200, detail
		}
		return 404, ``
	})
	if _, err := c.Search(context.Background(), "流浪地球2", "", 0); err != nil {
		t.Errorf("searchKind 兜底失败: %v", err)
	}

	// 完全没有结果
	c = fakeTMDB(t, func(string, url.Values) (int, string) { return 200, `{"results":[]}` })
	if _, err := c.Search(context.Background(), "不存在", "", 0); err == nil {
		t.Error("无结果应报错")
	}
}

func TestLookupByID(t *testing.T) {
	c := fakeTMDB(t, func(path string, _ url.Values) (int, string) {
		switch path {
		case "/movie/1":
			return 404, ``
		case "/tv/1":
			return 200, tvJSON
		}
		return 404, ``
	})
	cand, err := c.LookupByID(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if cand.MediaType != "tv" || cand.MediaLabel != "剧集" || cand.SiteURL != "https://www.themoviedb.org/tv/1" {
		t.Errorf("候选异常: %+v", cand)
	}

	// 指定类型只查该类型
	if _, err := c.LookupByID(context.Background(), 1, "movie"); err == nil {
		t.Error("指定 movie 但不存在应报错")
	}

	var nilClient *Client
	if _, err := nilClient.LookupByID(context.Background(), 1, ""); err == nil {
		t.Error("nil Client 应报错")
	}
}

func TestLookupByTitle(t *testing.T) {
	multi := `{"results":[{"id":55,"media_type":"movie","title":"流浪地球2","release_date":"2023-01-22"},{"id":66,"media_type":"person","name":"x"}]}`
	detail := `{"id":55,"title":"流浪地球2","release_date":"2023-01-22","overview":"简介","genres":[{"name":"科幻"}]}`

	c := fakeTMDB(t, func(path string, _ url.Values) (int, string) {
		switch path {
		case "/search/multi":
			return 200, multi
		case "/movie/55":
			return 200, detail
		}
		return 404, ``
	})
	out, err := c.LookupByTitle(context.Background(), "流浪地球2", 2023, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].TMDBID != 55 {
		t.Errorf("候选列表异常: %+v", out)
	}

	// limit<=0 走默认
	if _, err := c.LookupByTitle(context.Background(), "流浪地球2", 0, 0); err != nil {
		t.Errorf("limit=0 应正常: %v", err)
	}

	if _, err := c.LookupByTitle(context.Background(), "  ", 0, 5); err == nil {
		t.Error("空标题应报错")
	}

	c = fakeTMDB(t, func(string, url.Values) (int, string) { return 200, `{"results":[]}` })
	if _, err := c.LookupByTitle(context.Background(), "不存在", 0, 5); err == nil {
		t.Error("无结果应报错")
	}

	var nilClient *Client
	if _, err := nilClient.LookupByTitle(context.Background(), "x", 0, 5); err == nil {
		t.Error("nil Client 应报错")
	}
}

func TestSavePoster(t *testing.T) {
	c := fakeTMDB(t, func(path string, _ url.Values) (int, string) { return 200, "img-bytes" })

	// 正常下载
	r := &Result{MediaType: "movie", TMDBID: 1, PosterURL: "https://image.tmdb.org/t/p/w500/p.jpg"}
	got := c.SavePoster(context.Background(), r)
	if got == "" {
		t.Fatal("海报应下载成功")
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("海报文件不存在: %v", err)
	}

	// 已存在时直接返回同一路径
	if again := c.SavePoster(context.Background(), r); again != got {
		t.Errorf("重复下载应复用文件: %q != %q", again, got)
	}

	// 空结果 / 未启用缓存 / 无目录
	if p := c.SavePoster(context.Background(), nil); p != "" {
		t.Error("nil Result 应返回空串")
	}
	if p := c.SavePoster(context.Background(), &Result{PosterURL: ""}); p != "" {
		t.Error("空海报地址应返回空串")
	}
	noCache := *c
	noCache.cache = false
	if p := noCache.SavePoster(context.Background(), r); p != "" {
		t.Error("未启用缓存应返回空串")
	}

	// 下载失败 / 非 200
	bad := fakeTMDB(t, func(string, url.Values) (int, string) { return 500, "" })
	if p := bad.SavePoster(context.Background(), &Result{MediaType: "tv", TMDBID: 2, PosterURL: "https://image.tmdb.org/x"}); p != "" {
		t.Error("非 200 应返回空串")
	}

	// 超大响应应被拒绝
	huge := fakeTMDB(t, func(string, url.Values) (int, string) {
		return 200, strings.Repeat("x", (5<<20)+10)
	})
	if p := huge.SavePoster(context.Background(), &Result{MediaType: "tv", TMDBID: 3, PosterURL: "https://image.tmdb.org/y"}); p != "" {
		t.Error("超过大小上限应被拒绝")
	}
}

func TestHelpers(t *testing.T) {
	if !IsBearer("eyJhbGciOi") || !IsBearer(strings.Repeat("a", 65)) || IsBearer("short") {
		t.Error("IsBearer 判定错误")
	}

	if yearOf("") != 0 || yearOf("20") != 0 || yearOf("2023-01-01") != 2023 {
		t.Error("yearOf 判定错误")
	}

	if retryAfter("") != time.Second || retryAfter("abc") != time.Second || retryAfter("3") != 3*time.Second || retryAfter("0") != time.Second {
		t.Error("retryAfter 解析错误")
	}

	if MediaLabel("tv") != "剧集" || MediaLabel("movie") != "电影" || MediaLabel("") != "电影" {
		t.Error("MediaLabel 判定错误")
	}
	if siteURL("tv", 1) != "https://www.themoviedb.org/tv/1" || siteURL("movie", 2) != "https://www.themoviedb.org/movie/2" {
		t.Error("siteURL 拼接错误")
	}

	if normTitle("Hello 世界!") != "hello世界" {
		t.Errorf("normTitle = %q", normTitle("Hello 世界!"))
	}
	if bigramJaccard("abcd", "abcd") != 1 {
		t.Error("相同串相似度应为 1")
	}
	if bigramJaccard("", "x") != 0 {
		t.Error("空串相似度应为 0")
	}
	if bigramJaccard("abcdef", "abc") != 0.9 {
		t.Error("包含关系应给 0.9")
	}

	if yearScore(2020, 2020) != 1 || yearScore(2020, 2021) != 0.8 || yearScore(2020, 2022) != 0.4 || yearScore(2010, 2020) != 0 || yearScore(0, 2020) != 0.5 {
		t.Error("yearScore 判定错误")
	}

	if got := uniqueStrings([]string{" a ", "", "a", "b", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("uniqueStrings = %v", got)
	}
}

func TestValidateImageBase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "https://image.tmdb.org/t/p"},
		{"https://image.tmdb.org/t/p", "https://image.tmdb.org/t/p"},
		{"https://evil.example/t/p", "https://image.tmdb.org/t/p"},
		{"http://image.tmdb.org/t/p", "https://image.tmdb.org/t/p"}, // 必须 https
		{"not a url", "https://image.tmdb.org/t/p"},
	}
	for _, c := range cases {
		if got := validateImageBase(c.in); got != c.want {
			t.Errorf("validateImageBase(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestToResult_PrefersNameForTV(t *testing.T) {
	d := &detail{ID: 5, Title: "电影名", Name: "剧名", OriginalTitle: "ot", OriginalName: "on", FirstAirDate: "2019-05-01"}
	c := &Client{imageBase: "https://image.tmdb.org/t/p", posterSize: "w500"}
	r := d.toResult("tv", c)
	if r.Title != "剧名" || r.OriginalTitle != "on" || r.Year != 2019 {
		t.Errorf("tv 应优先取 name/original_name: %+v", r)
	}
	if r.PosterURL != "" {
		t.Error("无 poster_path 时不应生成海报地址")
	}
}

func TestToCandidate_TruncatesOverview(t *testing.T) {
	d := &detail{ID: 1, Title: "t", Overview: strings.Repeat("字", 200), ReleaseDate: "2020-01-01"}
	c := &Client{imageBase: "https://image.tmdb.org/t/p", posterSize: "w500"}
	cand := d.toCandidate("movie", c)
	runes := []rune(cand.Overview)
	if len(runes) != 160 || !strings.HasSuffix(cand.Overview, "…") {
		t.Errorf("简介应截断到 160 字符并以省略号结尾，实际 %d", len(runes))
	}
}

func TestNew(t *testing.T) {
	c, err := New(config.TMDBConfig{}, t.TempDir(), nil, nil)
	if err != nil || c != nil {
		t.Errorf("空 Key 应返回 nil,nil，实际 %v,%v", c, err)
	}

	c, err = New(config.TMDBConfig{
		APIKey:     "short-key",
		Language:   "en-US",
		ImageBase:  "https://evil.example/t/p", // 非法域名应回退默认
		PosterSize: "w780",
	}, t.TempDir(), nil, &config.TMDBRate{Enable: true, PerSecond: 4, Burst: 4})
	if err != nil {
		t.Fatal(err)
	}
	if c.bearer {
		t.Error("短 Key 不应判定为 bearer")
	}
	if c.imageBase != "https://image.tmdb.org/t/p" {
		t.Errorf("非法 image_base 未回退: %q", c.imageBase)
	}
	if c.language != "en-US" || c.posterSize != "w780" {
		t.Errorf("配置未生效: %+v", c)
	}

	// 空语言/尺寸走默认
	c, _ = New(config.TMDBConfig{APIKey: "eyJhbGciOiJIUzI1NiJ9.body.sig"}, "", nil, nil)
	if !c.bearer || c.language != "zh-CN" || c.posterSize != "w500" {
		t.Errorf("默认值异常: %+v", c)
	}

	// 代理地址非法时 New 报错
	if _, err := New(config.TMDBConfig{APIKey: "k"}, "", &config.ProxyConfig{Enable: true, Addr: "socks5://"}, nil); err == nil {
		t.Error("非法代理地址应报错")
	}
}

func TestTestConnection_EmptyKey(t *testing.T) {
	r := TestConnection(context.Background(), "  ", nil)
	if r.OK || !strings.Contains(r.Message, "API Key") {
		t.Errorf("空 Key 应提示未填写: %+v", r)
	}
}

func TestPathJoining(t *testing.T) {
	// 真实请求路径带 /3 前缀，fake 路由会剥掉它
	u, _ := url.Parse("https://api.themoviedb.org/3/movie/1?language=zh-CN")
	if strings.TrimPrefix(u.Path, "/3") != "/movie/1" {
		t.Errorf("path = %q", u.Path)
	}
	_ = filepath.Join("a", "b")
}
