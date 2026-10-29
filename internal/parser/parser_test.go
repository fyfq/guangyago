package parser

import (
	"strings"
	"testing"
)

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

func TestParseText_EmptyInput(t *testing.T) {
	entries, errs := ParseText("这里没有链接")
	if len(entries) != 0 {
		t.Errorf("应无条目，实际 %d", len(entries))
	}
	if len(errs) == 0 || errs[0].Err != "未找到光鸭云盘链接" {
		t.Errorf("应给出未找到链接的错误，实际 %+v", errs)
	}
}

func TestParseText_SingleLinkWithCode(t *testing.T) {
	text := `光鸭云盘用户给你分享了「流浪地球 (2019) {tmdb-421}」
https://www.guangyapan.com/s/AbC123_XyZ789?code=q7f2`
	entries, errs := ParseText(text)
	if len(errs) != 0 {
		t.Fatalf("不应有错误: %+v", errs)
	}
	if len(entries) != 1 {
		t.Fatalf("应解析出 1 条，实际 %d", len(entries))
	}
	e := entries[0]
	if e.ShareID != "AbC123" {
		t.Errorf("ShareID=%q", e.ShareID)
	}
	if e.Code != "q7f2" {
		t.Errorf("Code 应从 ?code= 参数取到，实际 %q", e.Code)
	}
	if e.Year != 2019 || e.TMDBID != 421 {
		t.Errorf("Year/TMDBID 应解析出 2019/421，实际 %d/%d", e.Year, e.TMDBID)
	}
}

func TestParseText_FallbackCodeFromNextLine(t *testing.T) {
	text := `分享：https://guangyapan.com/s/ID1_PWD  (无 code 参数)
提取码：abcd`
	entries, _ := ParseText(text)
	if len(entries) != 1 {
		t.Fatalf("应 1 条，实际 %d", len(entries))
	}
	if entries[0].Code != "abcd" {
		t.Errorf("Code 应从下一行提取，实际 %q", entries[0].Code)
	}
}

func TestParseText_DedupesByShareID(t *testing.T) {
	text := `https://guangyapan.com/s/AAA_1
https://guangyapan.com/s/AAA_2
https://guangyapan.com/s/BBB_3`
	entries, _ := ParseText(text)
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.ShareID] {
			t.Errorf("ShareID=%s 出现重复", e.ShareID)
		}
		seen[e.ShareID] = true
	}
	if len(entries) != 2 {
		t.Errorf("去重后应剩 2 条，实际 %d", len(entries))
	}
}

func TestParseCSV_Minimal(t *testing.T) {
	csv := "文件名,链接,提取码,分享时间,分享状态\n" +
		"阿凡达 (2009) {tmdb-19995},https://guangyapan.com/s/Ab1_CD2,xy12,2026-01-01 10:00:00,成功\n"
	entries, errs := ParseCSV(strings.NewReader(csv))
	if len(errs) != 0 {
		t.Fatalf("不应有错误: %+v", errs)
	}
	if len(entries) != 1 {
		t.Fatalf("应解析出 1 条，实际 %d", len(entries))
	}
	e := entries[0]
	if e.ShareID != "Ab1" || e.Code != "xy12" || e.Year != 2009 || e.TMDBID != 19995 {
		t.Errorf("字段解析异常: %+v", e)
	}
}

func TestParseCSV_Empty(t *testing.T) {
	_, errs := ParseCSV(strings.NewReader(""))
	if len(errs) == 0 || errs[0].Err != "CSV 内容为空" {
		t.Errorf("空 CSV 应给出内容空错误，实际 %+v", errs)
	}
}

func TestParseCSV_InvalidLink(t *testing.T) {
	csv := "文件名,链接\nabc,not-a-url\n"
	entries, errs := ParseCSV(strings.NewReader(csv))
	if len(entries) != 0 {
		t.Errorf("非光鸭链接不应入库，实际 %d", len(entries))
	}
	if len(errs) != 1 || errs[0].Err != "链接格式无法识别" {
		t.Errorf("应给出格式无法识别错误，实际 %+v", errs)
	}
}

func TestParseCSV_BOMStripped(t *testing.T) {
	csv := "\ufeff文件名,链接\nX,https://guangyapan.com/s/A_B\n"
	entries, errs := ParseCSV(strings.NewReader(csv))
	if len(errs) != 0 {
		t.Fatalf("带 BOM 的 CSV 应正常解析: %+v", errs)
	}
	if len(entries) != 1 || entries[0].ShareID != "A" {
		t.Errorf("BOM 未剥离导致表头错乱: %+v", entries)
	}
}
