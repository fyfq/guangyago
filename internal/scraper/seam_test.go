package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/odin/guangya-go/internal/config"
)

// seamTMDB 起一个假 TMDB，按请求路径返回固定 JSON，并记录最后一次请求的查询串。
func seamTMDB(t *testing.T, routes map[string]string) (*httptest.Server, func() string) {
	t.Helper()
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() string { return lastQuery }
}

const seamMovieDetail = `{"id":278,"title":"肖申克的救赎","original_title":"The Shawshank Redemption",
"release_date":"1994-09-10","overview":"希望是好事","vote_average":8.7,"runtime":142,
"poster_path":"/kXq.jpg","genres":[{"name":"剧情"},{"name":"犯罪"}]}`

func TestNewAtAPIBase_UsesInjectedBase(t *testing.T) {
	srv, query := seamTMDB(t, map[string]string{"/movie/278": seamMovieDetail})
	c, err := NewAtAPIBase(config.TMDBConfig{APIKey: "secret-key"}, t.TempDir(), nil, nil, srv.URL)
	if err != nil {
		t.Fatalf("NewAtAPIBase: %v", err)
	}
	if c == nil {
		t.Fatal("配置了 Key 不应返回 nil")
	}

	res, err := c.Fetch(context.Background(), 278, MatchRef{Title: "肖申克的救赎", Year: 1994, Hint: "movie"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Title != "肖申克的救赎" || res.Year != 1994 || res.Runtime != 142 {
		t.Errorf("刮削结果异常: %+v", res)
	}
	// 空 apiBase 必须回退官方地址，否则请求会拼成相对路径
	if c.apiBase != srv.URL {
		t.Errorf("apiBase = %q, 期望 %q", c.apiBase, srv.URL)
	}
	if !strings.Contains(query(), "api_key=secret-key") {
		t.Errorf("请求应带 api_key，实际 %q", query())
	}
}

func TestNewAtAPIBase_EmptyBaseFallsBack(t *testing.T) {
	c, err := NewAtAPIBase(config.TMDBConfig{APIKey: "k"}, t.TempDir(), nil, nil, "")
	if err != nil {
		t.Fatalf("NewAtAPIBase: %v", err)
	}
	if c.apiBase != defaultAPIBase {
		t.Errorf("空 apiBase 应回退官方地址，实际 %q", c.apiBase)
	}
}

func TestNewAtAPIBase_NoKeyReturnsNil(t *testing.T) {
	c, err := NewAtAPIBase(config.TMDBConfig{}, t.TempDir(), nil, nil, "https://example.invalid")
	if err != nil || c != nil {
		t.Errorf("无 Key 应返回 (nil, nil)，实际 %+v %v", c, err)
	}
}

func TestTestConnectionAt_Success(t *testing.T) {
	srv, _ := seamTMDB(t, map[string]string{
		"/configuration": `{"images":{"secure_base_url":"https://image.tmdb.org/t/p/","back_sizes":["w500"]}}`,
	})
	got := testConnectionAt(context.Background(), "secret-key", nil, srv.URL)
	if !got.OK {
		t.Fatalf("连接应判定成功: %+v", got)
	}
	if !strings.Contains(got.Message, "TMDB 连接正常") || !strings.Contains(got.Detail, "https://image.tmdb.org/t/p/") {
		t.Errorf("结果文案异常: %+v", got)
	}
}

func TestTestConnectionAt_BadKey(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(bad.Close)

	got := testConnectionAt(context.Background(), "secret-key", nil, bad.URL)
	if got.OK || !strings.Contains(got.Message, "API Key 无效") {
		t.Errorf("401 应提示 Key 无效: %+v", got)
	}

	got = testConnectionAt(context.Background(), "", nil, bad.URL)
	if got.OK || !strings.Contains(got.Message, "还没有填写") {
		t.Errorf("空 Key 应直接提示未填写: %+v", got)
	}
}

// TestNew_BuildsClientFromConfig 覆盖生产构造路径：New 必须落在官方根地址上。
func TestNew_BuildsClientFromConfig(t *testing.T) {
	c, err := New(config.TMDBConfig{APIKey: "k", Language: "en-US", PosterSize: "w780"}, t.TempDir(), nil, nil)
	if err != nil || c == nil {
		t.Fatalf("New: %+v %v", c, err)
	}
	if c.apiBase != defaultAPIBase || c.language != "en-US" || c.posterSize != "w780" {
		t.Errorf("New 配置未生效: %+v", c)
	}
}
