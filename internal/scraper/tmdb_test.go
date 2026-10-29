package scraper

import "testing"

func TestGuessMediaType(t *testing.T) {
	cases := []struct {
		name string
		text []string
		want string
	}{
		{"季标记", []string{"权力的游戏 第三季"}, "tv"},
		{"英文季集", []string{"Game.of.Thrones.S03E10.1080p"}, "tv"},
		{"仅季号", []string{"Stranger Things S04"}, "tv"},
		{"全X集", []string{"甄嬛传 全76集 国语"}, "tv"},
		{"运营词", []string{"某综艺 更新至第3期"}, "tv"},
		{"剧场版", []string{"鬼灭之刃 剧场版 无限列车篇"}, "movie"},
		{"英文电影", []string{"Inception.2010.1080p.BluRay"}, ""},
		{"无从判断", []string{"某资源"}, ""},
	}
	for _, c := range cases {
		if got := GuessMediaType(c.text...); got != c.want {
			t.Errorf("%s: GuessMediaType(%v) = %q, want %q", c.name, c.text, got, c.want)
		}
	}
}

func TestStripEpisodeMarkers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"权力的游戏 第三季", "权力的游戏"},
		{"Game of Thrones S03", "Game of Thrones"},
		{"甄嬛传 全76集", "甄嬛传"},
		{"Inception.2010", "Inception.2010"},
	}
	for _, c := range cases {
		if got := stripEpisodeMarkers(c.in); got != c.want {
			t.Errorf("stripEpisodeMarkers(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScoreCandidatePrefersSimilarTitleAndType(t *testing.T) {
	refTitle := "权力的游戏"
	raw := ""
	hits := []searchItem{
		{ID: 1, MediaType: "tv", Name: "权力的游戏", OriginalName: "Game of Thrones", FirstAirDate: "2011-04-17"},
		{ID: 2, MediaType: "movie", Name: "权力的游戏", OriginalName: "Game of Games", ReleaseDate: "2011-01-01"},
		{ID: 3, MediaType: "tv", Name: " totally different", FirstAirDate: "1990-01-01"},
	}
	tv := scoreCandidate(hits[0], 0, refTitle, raw, 2011, "tv")
	movie := scoreCandidate(hits[1], 1, refTitle, raw, 2011, "tv")
	unrelated := scoreCandidate(hits[2], 2, refTitle, raw, 2011, "tv")
	if tv <= movie {
		t.Errorf("剧集候选（%v）应高于错误类型候选（%v）", tv, movie)
	}
	if movie <= unrelated {
		t.Errorf("同标题候选（%v）应高于完全不相关候选（%v）", movie, unrelated)
	}
	if tv < goodEnoughScore {
		t.Errorf("标题年份类型全中时得分 %v 应达到 goodEnoughScore", tv)
	}
}

func TestScoreCandidateUsesOriginalTitle(t *testing.T) {
	it := searchItem{ID: 1, MediaType: "movie", Title: "千与千寻", OriginalTitle: "千と千尋の神隠し", ReleaseDate: "2001-07-20"}
	if s := scoreCandidate(it, 0, "千と千尋の神隠し", "", 2001, ""); s < goodEnoughScore {
		t.Errorf("原始语言标题应参与比对，得分 %v 过低", s)
	}
}

func TestNormalizeReleaseDate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"完整日期", "2024-06-01", "2024-06-01"},
		{"首尾空白", " 2024-06-01 ", "2024-06-01"},
		{"仅年份", "2024", ""},
		{"年月", "2024-06", ""},
		{"非闰年二月", "2023-02-29", ""},
		{"空值", "", ""},
	}
	for _, c := range cases {
		if got := normalizeReleaseDate(c.in); got != c.want {
			t.Errorf("%s: normalizeReleaseDate(%q) = %q, 期望 %q", c.name, c.in, got, c.want)
		}
	}
}

// 门户按上映时间排序依赖这个字段，movie 取 release_date、tv 取 first_air_date。
func TestToResultCarriesReleaseDate(t *testing.T) {
	c := &Client{imageBase: "https://image.tmdb.org/t/p", posterSize: "w500"}

	movie := (&detail{ID: 1, Title: "movie", ReleaseDate: "2023-11-08"}).toResult("movie", c)
	if movie.ReleaseDate != "2023-11-08" || movie.Year != 2023 {
		t.Errorf("电影应带上完整上映日期，实际 %+v", movie)
	}

	tv := (&detail{ID: 2, Name: "show", FirstAirDate: "2024"}).toResult("tv", c)
	if tv.ReleaseDate != "" {
		t.Errorf("残串日期应当未知处理，实际 %q", tv.ReleaseDate)
	}
	if tv.Year != 2024 {
		t.Errorf("年份未知日期时仍应保留，实际 %d", tv.Year)
	}
}
