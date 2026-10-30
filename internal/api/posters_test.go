package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/odin/guangya-go/internal/model"
)

func TestPosterNameRe(t *testing.T) {
	for _, name := range []string{"movie-550.jpg", "tv-1399.jpg", "movie-1.jpg"} {
		if !posterNameRe.MatchString(name) {
			t.Errorf("%q 应被放行", name)
		}
	}
	for _, name := range []string{
		"", "poster.jpg", "movie-550.png", "movie-.jpg", "movie-550.jpg.tmp",
		"../movie-550.jpg", "movie-550.jpg/x", "movie-1234567890123.jpg", "Movie-550.jpg",
	} {
		if posterNameRe.MatchString(name) {
			t.Errorf("%q 不应被放行", name)
		}
	}
}

func TestPosterFile(t *testing.T) {
	env := newAPIEnv(t)
	dir := t.TempDir()
	env.cfg.Storage.PosterDir = dir
	content := []byte("\xff\xd8\xff\xe0jpeg-bytes")
	if err := os.WriteFile(filepath.Join(dir, "movie-550.jpg"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	rec := env.callAnon(http.MethodGet, "/posters/movie-550.jpg", nil)
	requireStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q，期望 image/jpeg", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
		t.Errorf("Cache-Control = %q，应带 max-age", cc)
	}
	if rec.Body.String() != string(content) {
		t.Error("响应内容与缓存文件不一致")
	}

	for _, name := range []string{"movie-551.jpg", "movie-550.png", "secret.jpg"} {
		requireStatus(t, env.callAnon(http.MethodGet, "/posters/"+name, nil), http.StatusNotFound)
	}
}

func TestPortalPosterPrefersCachedFile(t *testing.T) {
	env := newAPIEnv(t)
	dir := t.TempDir()
	env.cfg.Storage.PosterDir = dir
	if err := os.WriteFile(filepath.Join(dir, "movie-129.jpg"), []byte("\xff\xd8jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}

	const remote = "https://image.tmdb.org/t/p/w500/abc.jpg"
	cached := env.seedPublishedLink("s1", &model.Link{
		Title: "千与千寻", MediaType: model.MediaMovie, TMDBID: 129, PosterURL: remote,
	})
	env.seedPublishedLink("s2", &model.Link{
		Title: "没有缓存的海报", MediaType: model.MediaMovie, TMDBID: 130, PosterURL: remote,
	})

	rec := env.callAnon(http.MethodGet, "/api/portal/links", nil)
	requireStatus(t, rec, http.StatusOK)
	var body struct {
		Items []model.PublicLink `json:"items"`
	}
	decodeInto(t, rec, &body)
	if len(body.Items) != 2 {
		t.Fatalf("条目数 = %d，期望 2", len(body.Items))
	}
	byTMDB := map[int64]string{}
	for _, it := range body.Items {
		byTMDB[it.TMDBID] = it.PosterURL
	}
	if got := byTMDB[129]; got != "/posters/movie-129.jpg" {
		t.Errorf("有缓存时 poster_url = %q，期望同源路径", got)
	}
	if got := byTMDB[130]; got != remote {
		t.Errorf("无缓存时 poster_url = %q，期望回退远端地址", got)
	}

	single := env.callAnon(http.MethodGet, "/api/portal/link/"+idPath(cached.ID), nil)
	requireStatus(t, single, http.StatusOK)
	var item model.PublicLink
	decodeInto(t, single, &item)
	if item.PosterURL != "/posters/movie-129.jpg" {
		t.Errorf("单条接口 poster_url = %q，期望同源路径", item.PosterURL)
	}
}

func TestAbsolutePoster(t *testing.T) {
	cases := []struct{ base, poster, want string }{
		{"https://gy.example.com", "/posters/movie-1.jpg", "https://gy.example.com/posters/movie-1.jpg"},
		{"https://gy.example.com/", "/posters/movie-1.jpg", "https://gy.example.com/posters/movie-1.jpg"},
		{"https://gy.example.com", "https://image.tmdb.org/x.jpg", "https://image.tmdb.org/x.jpg"},
		{"https://gy.example.com", "", ""},
	}
	for _, c := range cases {
		if got := absolutePoster(c.base, c.poster); got != c.want {
			t.Errorf("absolutePoster(%q, %q) = %q，期望 %q", c.base, c.poster, got, c.want)
		}
	}
}
