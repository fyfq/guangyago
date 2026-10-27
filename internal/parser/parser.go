package parser

import (
	"encoding/csv"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Source 标识网盘来源。
const (
	SourceGuangya = "guangya"
)

// Entry 是从分享文本或 CSV 中解析出的单条记录。
type Entry struct {
	ShareID     string
	URL         string
	Code        string
	Source      string
	RawTitle    string
	Title       string
	Year        int
	TMDBID      int64
	SharedAt    string
	ShareStatus string
	Line        int // CSV 行号，从 2 开始；文本解析为 0
}

// RowError 描述一行解析失败的原因。
type RowError struct {
	Line int
	Raw  string
	Err  string
}

var (
	urlRe       = regexp.MustCompile(`https?://(?:www\.)?guangyapan\.com/s/([A-Za-z0-9]+)_([A-Za-z0-9]+)(?:\?[^\s"']*?code=([A-Za-z0-9]+))?`)
	tmdbRe      = regexp.MustCompile(`(?i)\{\s*tmdb-(\d+)\s*\}`)
	stubIDRe    = regexp.MustCompile(`[…\.]{1,3}-(\d{3,8})\}?\s*$`)
	titleYearRe = regexp.MustCompile(`^(.+?)\s*[（(]\s*(\d{4})\s*[)）]`)
	codeLineRe  = regexp.MustCompile(`提取码[:：]\s*([A-Za-z0-9]+)`)
	quoteRe     = regexp.MustCompile(`[「『“"]([^」』“”"]+)[」』“”"]`)
	noiseRe     = regexp.MustCompile(`(?i)(?:\b(?:2160p|1080p|720p|4k|8k|x264|x265|h264|h265|hevc|hdr|dv|10bit|aac|ac3|dts|bluray|blu-?ray|webrip|web-?dl|remux|hdr10\+?)\b|高清|超清|国语|中字|双字|双语|字幕组|完整版|未删减版?|修复版|加长版|蓝光|抢先版)`)
	bracketRe   = regexp.MustCompile(`[\[\]【】\{\}（）()]`)
)

// ParseText 解析一段或多段光鸭分享文本，返回全部链接条目。
func ParseText(text string) ([]Entry, []RowError) {
	entries := []Entry{}
	errs := []RowError{}
	matches := urlRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return entries, append(errs, RowError{Err: "未找到光鸭云盘链接"})
	}
	// 提取码可能出现在链接之后的下一行
	fallbackCode := ""
	if m := codeLineRe.FindStringSubmatch(text); len(m) == 2 {
		fallbackCode = m[1]
	}
	quoted := ""
	if m := quoteRe.FindStringSubmatch(text); len(m) == 2 {
		quoted = m[1]
	}

	for _, m := range matches {
		e := Entry{
			ShareID: m[1],
			URL:     m[0],
			Source:  SourceGuangya,
		}
		if len(m) == 4 {
			e.Code = m[3]
		}
		if e.Code == "" {
			e.Code = fallbackCode
		}
		e.RawTitle = strings.TrimSpace(quoted)
		title, year, tmdbID := splitTitle(e.RawTitle)
		e.Title = title
		e.Year = year
		e.TMDBID = tmdbID
		entries = append(entries, e)
	}
	return dedupe(entries), errs
}

// ParseCSV 解析光鸭批量分享记录 CSV，列头为：文件名,链接,提取码,分享时间,分享状态。
func ParseCSV(r io.Reader) ([]Entry, []RowError) {
	errs := []RowError{}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, []RowError{{Err: "读取内容失败: " + err.Error()}}
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(raw), "\ufeff")))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, []RowError{{Err: "CSV 解析失败: " + err.Error()}}
	}
	if len(records) == 0 {
		return nil, []RowError{{Err: "CSV 内容为空"}}
	}

	header := indexHeader(records[0])
	entries := []Entry{}
	for i, rec := range records[1:] {
		line := i + 2
		get := func(key string) string {
			idx, ok := header[key]
			if !ok || idx >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[idx])
		}
		raw := get("文件名")
		url := get("链接")
		if raw == "" && url == "" {
			continue
		}
		e := Entry{
			RawTitle:    raw,
			URL:         url,
			Code:        get("提取码"),
			SharedAt:    get("分享时间"),
			ShareStatus: get("分享状态"),
			Source:      SourceGuangya,
			Line:        line,
		}
		m := urlRe.FindStringSubmatch(url)
		if len(m) == 0 {
			errs = append(errs, RowError{Line: line, Raw: url, Err: "链接格式无法识别"})
			continue
		}
		e.ShareID = m[1]
		if e.Code == "" && len(m) == 4 {
			e.Code = m[3]
		}
		title, year, tmdbID := splitTitle(raw)
		e.Title = title
		e.Year = year
		e.TMDBID = tmdbID
		entries = append(entries, e)
	}
	return dedupe(entries), errs
}

func indexHeader(rec []string) map[string]int {
	out := make(map[string]int, len(rec))
	for i, h := range rec {
		// 剥离 BOM 与空白
		h = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		h = strings.Trim(h, `"'`)
		out[h] = i
	}
	return out
}

// splitTitle 把 "Benjamin Franklin (2022) {tmdb-156138}" 拆成标题、年份、TMDB ID。
// SplitTitle 导出版：把「标题 (年份) {tmdb-ID}」拆成标题、年份与 TMDB ID。
// 刮削会覆写库里的 title/year，需要回溯原始片名时可拿 raw_title 重新解析。
func SplitTitle(raw string) (title string, year int, tmdbID int64) {
	return splitTitle(raw)
}

func splitTitle(raw string) (title string, year int, tmdbID int64) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", 0, 0
	}
	if m := tmdbRe.FindStringSubmatch(s); len(m) == 2 {
		tmdbID, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if tmdbID == 0 {
		// 分享文本中的截断形态：…标题-156138}
		if m := stubIDRe.FindStringSubmatch(s); len(m) == 2 {
			tmdbID, _ = strconv.ParseInt(m[1], 10, 64)
		}
	}
	s = tmdbRe.ReplaceAllString(s, "")
	s = stubIDRe.ReplaceAllString(s, "")
	if m := titleYearRe.FindStringSubmatch(s); len(m) == 3 {
		title = strings.TrimSpace(m[1])
		year, _ = strconv.Atoi(m[2])
	} else {
		title = strings.TrimSpace(s)
	}
	title = CleanTitle(title)
	return title, year, tmdbID
}

// CleanTitle 去掉水印、分辨率、编码与字幕组标记，只保留可用于搜索的片名。
func CleanTitle(s string) string {
	out := bracketRe.ReplaceAllString(s, " ")
	out = noiseRe.ReplaceAllString(out, " ")
	out = strings.NewReplacer("·", " ", "|", " ", "_", " ").Replace(out)
	return strings.Trim(strings.Join(strings.Fields(out), " "), " -")
}

var ErrNoLink = errors.New("未找到可解析的光鸭链接")

// dedupe 按 share_id 去重，保留首次出现的记录。
func dedupe(in []Entry) []Entry {
	seen := make(map[string]struct{}, len(in))
	out := make([]Entry, 0, len(in))
	for _, e := range in {
		key := e.ShareID
		if key == "" {
			key = e.URL
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	return out
}
