package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/odin/gygo/internal/config"
)

// 图片只按内容嗅探判定格式，所以夹具只需要带上真实签名头，不必是完整可解码的图片。
var (
	brandingPNG  = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{0x20}, 32)...)
	brandingJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x20}, 32)...)
	brandingSVG  = []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`)
)

// brandingEnv 给出一个品牌接口可用的最小服务端：配置落在临时目录，不碰真实 data 目录。
type brandingEnv struct {
	s       *Server
	dir     string // branding 素材目录
	cfgPath string // config.yaml 路径
}

func newBrandingEnv(t *testing.T) *brandingEnv {
	t.Helper()
	s, _ := newSEOServer(t)
	root := t.TempDir()
	s.configPath = filepath.Join(root, "config.yaml")
	s.cfg.Storage.DataDir = root
	s.cfg.SEO.SiteName = "测试站"
	return &brandingEnv{s: s, dir: s.cfg.BrandingDir(), cfgPath: s.configPath}
}

// upload 发一次真实的 multipart 上传，返回状态码与响应体。
func (e *brandingEnv) upload(t *testing.T, files map[string][]byte, persist bool) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, kind := range []string{"logo", "icon"} {
		data, ok := files[kind]
		if !ok {
			continue
		}
		part, err := mw.CreateFormFile(kind, kind+".bin")
		if err != nil {
			t.Fatalf("构造表单失败: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("写入表单字段失败: %v", err)
		}
	}
	if persist {
		if err := mw.WriteField("save_to_file", "true"); err != nil {
			t.Fatalf("写入表单字段失败: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭表单失败: %v", err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/branding", &body)
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())
	e.s.brandingUpload(c)
	return rec.Code, rec.Body.String()
}

func (e *brandingEnv) clear(t *testing.T, kind string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/branding?kind="+kind, nil)
	e.s.brandingClear(c)
	return rec.Code, rec.Body.String()
}

// read 用真实 handler 取 /branding/:name，params 走 gin 的路径参数。
func (e *brandingEnv) read(t *testing.T, name string) (int, string, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "name", Value: name}}
	c.Request = httptest.NewRequest(http.MethodGet, "/branding/"+name, nil)
	e.s.brandingFile(c)
	return rec.Code, rec.Body.String(), rec.Header()
}

func (e *brandingEnv) file(name string) []byte {
	data, err := os.ReadFile(filepath.Join(e.dir, name))
	if err != nil {
		return nil
	}
	return data
}

func TestBrandingUpload_WritesFilesAndConfig(t *testing.T) {
	e := newBrandingEnv(t)
	code, body := e.upload(t, map[string][]byte{"logo": brandingPNG, "icon": brandingJPEG}, true)
	if code != http.StatusOK {
		t.Fatalf("上传应成功，实际 %d: %s", code, body)
	}
	for _, want := range []string{`"site_name":"测试站"`, `"logo_url":"/branding/logo.png?v=`, `"icon_url":"/branding/icon.jpg?v=`} {
		if !strings.Contains(body, want) {
			t.Errorf("回包缺少 %q，实际: %s", want, body)
		}
	}
	if !bytes.Equal(e.file("logo.png"), brandingPNG) {
		t.Error("logo 落盘内容与上传不一致")
	}
	if !bytes.Equal(e.file("icon.jpg"), brandingJPEG) {
		t.Error("icon 落盘内容与上传不一致")
	}

	saved, err := config.Load(e.cfgPath)
	if err != nil {
		t.Fatalf("读取写回的配置文件失败: %v", err)
	}
	if saved.SEO.LogoFile != "logo.png" || saved.SEO.IconFile != "icon.jpg" {
		t.Errorf("配置未记录文件名: logo=%q icon=%q", saved.SEO.LogoFile, saved.SEO.IconFile)
	}
}

func TestBrandingUpload_KeepsRuntimeOnlyWhenNotPersisting(t *testing.T) {
	e := newBrandingEnv(t)
	if code, body := e.upload(t, map[string][]byte{"logo": brandingPNG}, false); code != http.StatusOK {
		t.Fatalf("上传应成功，实际 %d: %s", code, body)
	}
	if _, err := os.Stat(e.cfgPath); !os.IsNotExist(err) {
		t.Error("未勾选写入配置文件时不应生成 config.yaml")
	}
	if e.s.cfg.SEO.LogoFile != "logo.png" {
		t.Errorf("内存配置应即时生效，实际 %q", e.s.cfg.SEO.LogoFile)
	}
}

func TestBrandingUpload_ReplacesOldFile(t *testing.T) {
	e := newBrandingEnv(t)
	if code, body := e.upload(t, map[string][]byte{"logo": brandingPNG}, false); code != http.StatusOK {
		t.Fatalf("首次上传应成功，实际 %d: %s", code, body)
	}
	e.s.cfg.SEO.LogoFile = "logo.png"
	if code, body := e.upload(t, map[string][]byte{"logo": brandingJPEG}, false); code != http.StatusOK {
		t.Fatalf("换图应成功，实际 %d: %s", code, body)
	}
	if e.file("logo.png") != nil {
		t.Error("格式变化后旧文件应被删除")
	}
	if e.file("logo.jpg") == nil {
		t.Error("新文件应落盘")
	}
}

func TestBrandingUpload_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"缺文件", map[string][]byte{}, "缺少 logo 或 icon 文件"},
		{"非图片", map[string][]byte{"logo": []byte("hello world, not an image")}, "只支持"},
		{"SVG", map[string][]byte{"logo": brandingSVG}, "只支持"},
		{"图标超限", map[string][]byte{"icon": append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
			bytes.Repeat([]byte{0x20}, maxIconBytes)...)}, "超过"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newBrandingEnv(t)
			code, body := e.upload(t, c.files, false)
			if code != http.StatusBadRequest {
				t.Errorf("应返回 400，实际 %d: %s", code, body)
			}
			if !strings.Contains(body, c.want) {
				t.Errorf("错误信息应含 %q，实际: %s", c.want, body)
			}
			if entries, _ := os.ReadDir(e.dir); len(entries) != 0 {
				t.Error("被拒绝的上传不应留下素材文件")
			}
		})
	}
}

func TestBrandingFile_ServesOnlyConfiguredNames(t *testing.T) {
	e := newBrandingEnv(t)
	if code, body := e.upload(t, map[string][]byte{"logo": brandingPNG, "icon": brandingJPEG}, false); code != http.StatusOK {
		t.Fatalf("上传应成功，实际 %d: %s", code, body)
	}

	code, body, head := e.read(t, "logo.png")
	if code != http.StatusOK {
		t.Fatalf("读取 logo 应成功，实际 %d: %s", code, body)
	}
	if ct := head.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q，期望 image/png", ct)
	}
	if cc := head.Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if body != string(brandingPNG) {
		t.Error("响应体与落盘内容不一致")
	}

	for _, name := range []string{"icon.gif", "config.yaml", "db.sqlite", "..%2Fconfig.yaml"} {
		if code, _, _ := e.read(t, name); code != http.StatusNotFound {
			t.Errorf("%s 不应被提供服务，实际 %d", name, code)
		}
	}
}

func TestBrandingFile_MissingOnDiskIs404(t *testing.T) {
	e := newBrandingEnv(t)
	e.s.cfg.SEO.IconFile = "icon.png" // 配置有记录，文件已被手工删掉
	if code, _, _ := e.read(t, "icon.png"); code != http.StatusNotFound {
		t.Errorf("文件缺失应 404，实际 %d", code)
	}
}

func TestBrandingClear_RemovesFileAndField(t *testing.T) {
	e := newBrandingEnv(t)
	if code, body := e.upload(t, map[string][]byte{"logo": brandingPNG, "icon": brandingJPEG}, true); code != http.StatusOK {
		t.Fatalf("上传应成功，实际 %d: %s", code, body)
	}
	code, body := e.clear(t, "logo")
	if code != http.StatusOK {
		t.Fatalf("清除应成功，实际 %d: %s", code, body)
	}
	if e.s.cfg.SEO.LogoFile != "" {
		t.Errorf("配置里的 logo 文件名应清空，实际 %q", e.s.cfg.SEO.LogoFile)
	}
	if e.file("logo.png") != nil {
		t.Error("素材文件应删除")
	}
	if !strings.Contains(body, `"logo_url":""`) {
		t.Errorf("回包应给出空 logo 地址，实际: %s", body)
	}
	if e.s.cfg.SEO.IconFile != "icon.jpg" {
		t.Errorf("清除 logo 不应影响 icon，实际 %q", e.s.cfg.SEO.IconFile)
	}
}

func TestBrandingClear_RejectsUnknownKind(t *testing.T) {
	e := newBrandingEnv(t)
	if code, _ := e.clear(t, "watermark"); code != http.StatusBadRequest {
		t.Errorf("非法 kind 应 400，实际 %d", code)
	}
}

func TestBrandingState_DefaultsAndNilConfig(t *testing.T) {
	e := newBrandingEnv(t)
	body, _ := callHandler(t, e.s.brandingState, "/api/branding")
	for _, want := range []string{`"site_name":"测试站"`, `"logo_url":""`, `"icon_url":""`} {
		if !strings.Contains(body, want) {
			t.Errorf("未上传素材时回包应含 %q，实际: %s", want, body)
		}
	}

	// 配置未加载也不能报错：站点名退回默认值，素材地址为空。
	bare := &Server{}
	body, _ = callHandler(t, bare.brandingState, "/api/branding")
	if !strings.Contains(body, `"site_name":"gygo"`) || !strings.Contains(body, `"logo_url":""`) {
		t.Errorf("空配置回包异常: %s", body)
	}
}

func TestWriteIndex_SiteNameAndIcon(t *testing.T) {
	e := newBrandingEnv(t)
	if code, body := e.upload(t, map[string][]byte{"icon": brandingPNG}, false); code != http.StatusOK {
		t.Fatalf("上传应成功，实际 %d: %s", code, body)
	}
	doc, _ := writeIndex(e.s, "/", `<title>{{SITE_NAME}} - 网盘资源分享平台</title>`)
	if !strings.Contains(doc, "<title>测试站 - 网盘资源分享平台</title>") {
		t.Errorf("站点名未替换: %s", doc)
	}
	if strings.Contains(doc, "{{SITE_NAME}}") {
		t.Errorf("模板占位符残留: %s", doc)
	}
	if !strings.Contains(doc, `<link rel="icon" href="/branding/icon.png?v=`) {
		t.Errorf("未注入站点图标: %s", doc)
	}
}

func TestWriteIndex_EscapesSiteName(t *testing.T) {
	e := newBrandingEnv(t)
	e.s.cfg.SEO.SiteName = `x"><script>alert(1)</script>`
	doc, _ := writeIndex(e.s, "/", `<title>{{SITE_NAME}}</title>`)
	if strings.Contains(doc, "<script>alert(1)</script>") {
		t.Errorf("站点名未转义，可注入标签: %s", doc)
	}
	if !strings.Contains(doc, "&#34;") {
		t.Errorf("站点名里的引号应被转义: %s", doc)
	}
}

func TestBrandingExt_SniffsRealFormat(t *testing.T) {
	cases := []struct {
		data []byte
		want string
		ok   bool
	}{
		{brandingPNG, ".png", true},
		{brandingJPEG, ".jpg", true},
		{[]byte("GIF89a........"), ".gif", true},
		{[]byte("RIFFxxxxWEBPVP8 "), ".webp", true},
		{brandingSVG, "", false},
		{[]byte("plain text file"), "", false},
	}
	for _, c := range cases {
		ext, ok := brandingExt(c.data)
		if ok != c.ok || ext != c.want {
			t.Errorf("brandingExt(%.20q) = %q,%v 期望 %q,%v", c.data, ext, ok, c.want, c.ok)
		}
	}
}

func TestReadBrandingFile_RejectsEmptyAndOversize(t *testing.T) {
	headers := brandingFormHeaders(t, map[string][]byte{"logo": {}, "icon": brandingPNG})

	if _, err := readBrandingFile(headers["logo"], maxLogoBytes); err == nil || !strings.Contains(err.Error(), "内容为空") {
		t.Errorf("空文件应拒绝，实际: %v", err)
	}
	if _, err := readBrandingFile(headers["icon"], 8); err == nil || !strings.Contains(err.Error(), "超过") {
		t.Errorf("超过上限应拒绝，实际: %v", err)
	}
	// Size 造假：声明很小、实际很大，仍然要在读满 limit+1 后拦住。
	spoofed := *headers["icon"]
	spoofed.Size = 4
	if _, err := readBrandingFile(&spoofed, 8); err == nil || !strings.Contains(err.Error(), "超过") {
		t.Errorf("Size 造假应被 LimitReader 拦住，实际: %v", err)
	}
	got, err := readBrandingFile(headers["icon"], maxIconBytes)
	if err != nil || !bytes.Equal(got, brandingPNG) {
		t.Errorf("正常文件应完整读出，got=%.20q err=%v", got, err)
	}
}

// brandingFormHeaders 走真实 multipart 解析拿到 FileHeader，
// 这样测试覆盖的是 handler 实际拿到的结构，而不是手工拼的假对象。
func brandingFormHeaders(t *testing.T, files map[string][]byte) map[string]*multipart.FileHeader {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, kind := range []string{"logo", "icon"} {
		data, ok := files[kind]
		if !ok {
			continue
		}
		part, err := mw.CreateFormFile(kind, kind+".bin")
		if err != nil {
			t.Fatalf("构造表单失败: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("写入表单字段失败: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭表单失败: %v", err)
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/branding", &body)
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())

	out := make(map[string]*multipart.FileHeader, len(files))
	for kind := range files {
		f, err := c.FormFile(kind)
		if err != nil {
			t.Fatalf("解析 %s 表单字段失败: %v", kind, err)
		}
		out[kind] = f
	}
	return out
}
