package parser

import "testing"

func TestCleanTitleStripsChineseNoise(t *testing.T) {
	cases := []struct{ in, want string }{
		{"流浪地球2 [4K HDR 中字]", "流浪地球2"},
		{"肖申克的救赎 1080p 高清 未删减版", "肖申克的救赎"},
		{"千与千寻 国语双语 蓝光", "千与千寻"},
		{"正常片名", "正常片名"},
	}
	for _, c := range cases {
		if got := CleanTitle(c.in); got != c.want {
			t.Errorf("CleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitTitleKeepsYearAndID(t *testing.T) {
	title, year, id := SplitTitle("Benjamin Franklin (2022) {tmdb-156138}")
	if title != "Benjamin Franklin" || year != 2022 || id != 156138 {
		t.Errorf("got (%q, %d, %d), want (Benjamin Franklin, 2022, 156138)", title, year, id)
	}
}
