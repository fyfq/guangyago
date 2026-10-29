package api

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/config"
)

const (
	// maxLogoBytes / maxIconBytes 是两类素材的大小上限：logo 用在页面头部，
	// 站点图标只需要小图，收紧上限可以避免误传原图把 data 目录撑大。
	maxLogoBytes = 512 << 10
	maxIconBytes = 256 << 10
	// brandingDirMode 沿用数据目录的权限：进程属主可写，组内可读。
	// 素材只由本进程读取后回源给浏览器，落盘权限收到属主可读，不必对 other 开放。
	brandingDirMode  = 0o750
	brandingFileMode = 0o600
	// brandingMaxAgeSeconds 是素材的浏览器缓存时长；换图后 URL 会带新的 ?v= 版本号，
	// 所以缓存可以放久一点。
	brandingMaxAgeSeconds = 86400
)

// brandingImageTypes 把嗅探到的 MIME 映射成落盘扩展名。
// 刻意不收 svg：svg 可以内嵌脚本，同源读取等于把站内多了一个可执行入口。
var brandingImageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// brandingContentTypes 按扩展名给出响应类型，与服务端写入时的判定保持一致。
var brandingContentTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// brandingPayload 是前端与原始 HTML 共用的品牌信息：站点标题、logo、站点图标。
type brandingPayload struct {
	SiteName     string `json:"site_name"`
	SiteSubtitle string `json:"site_subtitle"`
	LogoURL      string `json:"logo_url"`
	IconURL      string `json:"icon_url"`
}

// brandingSnapshot 读取品牌配置。站点名沿用 siteName() 的默认值兜底，
// logo/icon 为空表示用前端内置标识。
func (s *Server) brandingSnapshot() (siteName, logoFile, iconFile, dir string) {
	s.configMu.RLock()
	site, logo, icon := "", "", ""
	if s.cfg != nil {
		site = strings.TrimSpace(s.cfg.SEO.SiteName)
		logo = strings.TrimSpace(s.cfg.SEO.LogoFile)
		icon = strings.TrimSpace(s.cfg.SEO.IconFile)
		dir = s.cfg.BrandingDir()
	}
	s.configMu.RUnlock()
	if site == "" {
		site = defaultSiteName
	}
	return site, logo, icon, dir
}

// brandingHref 拼出素材的同源访问路径，并按文件修改时间加版本号，
// 这样后台换图后浏览器立刻拉到新文件，不必强刷。
func brandingHref(dir, file string) string {
	if file == "" {
		return ""
	}
	out := "/branding/" + file
	if st, err := os.Stat(filepath.Join(dir, file)); err == nil {
		out += "?v=" + strconv.FormatInt(st.ModTime().Unix(), 10)
	}
	return out
}

// brandingState 供门户、登录页与后台读取站点标题 / logo / 图标。
// 这些值本来就会出现在公开页面的 HTML 里，因此不需要鉴权。
func (s *Server) brandingState(c *gin.Context) {
	c.JSON(http.StatusOK, s.currentBranding())
}

// brandingHeadTags 给原始 HTML 补一条站点图标链接。
// 前端运行时也会设置一次，但爬虫与首屏源码里有一份更稳，
// 且 CSP 禁内联脚本，无法用 JS 之外的方式在模板里写死动态 href。
func (s *Server) brandingHeadTags() string {
	_, _, icon, dir := s.brandingSnapshot()
	href := brandingHref(dir, icon)
	if href == "" {
		return ""
	}
	return `<link rel="icon" href="` + htmlEscape(href) + `" />`
}

// siteNamePlaceholder 是 index.html 模板里的站点名占位符，由服务端在返回首页时替换。
const siteNamePlaceholder = "{{SITE_NAME}}"

// applySiteName 替换模板里的站点名占位符。站点名是后台输入，按 HTML 转义后再拼，
// 否则可以闭合 <title> 往 head 里塞标签。
func (s *Server) applySiteName(doc string) string {
	if !strings.Contains(doc, siteNamePlaceholder) {
		return doc
	}
	return strings.ReplaceAll(doc, siteNamePlaceholder, htmlEscape(s.siteName()))
}

// currentBranding 组装当前品牌信息，GET 接口与上传成功后的回包共用。
func (s *Server) currentBranding() brandingPayload {
	site, logo, icon, dir := s.brandingSnapshot()
	s.configMu.RLock()
	subtitle := "资源库"
	if s.cfg != nil {
		subtitle = strings.TrimSpace(s.cfg.SEO.SiteSubtitle)
	}
	s.configMu.RUnlock()
	return brandingPayload{
		SiteSubtitle: subtitle,
		SiteName:     site,
		LogoURL:      brandingHref(dir, logo),
		IconURL:      brandingHref(dir, icon),
	}
}

// brandingUpload 接收 multipart 里的 logo / icon，按内容嗅探判定格式后落盘，
// 并把文件名写进配置（勾选写入配置文件时同步 config.yaml）。
func (s *Server) brandingUpload(c *gin.Context) {
	type pendingFile struct {
		kind string
		name string
		data []byte
	}
	limits := map[string]int{"logo": maxLogoBytes, "icon": maxIconBytes}

	var writes []pendingFile
	for _, kind := range []string{"logo", "icon"} {
		f, err := c.FormFile(kind)
		if errors.Is(err, http.ErrMissingFile) {
			continue
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "读取上传文件失败"})
			return
		}
		data, err := readBrandingFile(f, limits[kind])
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ext, ok := brandingExt(data)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "只支持 PNG / JPEG / GIF / WebP 图片"})
			return
		}
		writes = append(writes, pendingFile{kind: kind, name: kind + ext, data: data})
	}
	if len(writes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 logo 或 icon 文件"})
		return
	}

	_, _, _, dir := s.brandingSnapshot()
	if dir == "" {
		serverError(c, errors.New("未配置数据目录"))
		return
	}
	if err := os.MkdirAll(dir, brandingDirMode); err != nil {
		serverError(c, err)
		return
	}

	s.configMu.Lock()
	cfg := s.cfg
	if cfg == nil {
		s.configMu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "配置未加载"})
		return
	}
	for _, w := range writes {
		if err := writeBrandingFile(dir, w.name, w.data); err != nil {
			s.configMu.Unlock()
			serverError(c, err)
			return
		}
		old := ""
		switch w.kind {
		case "logo":
			old, cfg.SEO.LogoFile = cfg.SEO.LogoFile, w.name
		case "icon":
			old, cfg.SEO.IconFile = cfg.SEO.IconFile, w.name
		}
		if old != "" && old != w.name {
			removeBrandingFile(dir, old)
		}
		slog.Info("站点素材已更新", "kind", w.kind, "file", w.name)
	}
	saveErr := saveBrandingConfig(c, s.configPath, cfg)
	s.configMu.Unlock()

	if saveErr != nil {
		serverError(c, saveErr)
		return
	}
	c.JSON(http.StatusOK, s.currentBranding())
}

// brandingClear 删除一类素材文件，并清空配置里的引用。
func (s *Server) brandingClear(c *gin.Context) {
	kind := c.Query("kind")
	if kind != "logo" && kind != "icon" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind 只支持 logo / icon"})
		return
	}
	_, _, _, dir := s.brandingSnapshot()

	s.configMu.Lock()
	cfg := s.cfg
	if cfg == nil {
		s.configMu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "配置未加载"})
		return
	}
	name := ""
	switch kind {
	case "logo":
		name, cfg.SEO.LogoFile = cfg.SEO.LogoFile, ""
	case "icon":
		name, cfg.SEO.IconFile = cfg.SEO.IconFile, ""
	}
	saveErr := saveBrandingConfig(c, s.configPath, cfg)
	s.configMu.Unlock()

	if saveErr != nil {
		serverError(c, saveErr)
		return
	}
	if name != "" {
		removeBrandingFile(dir, name)
	}
	slog.Info("站点素材已清除", "kind", kind)
	s.brandingState(c)
}

// brandingFile 只提供配置里记录的那个 logo / icon 文件：文件名要与配置精确相等，
// 因此既不可能路径穿越，也不会把 data 目录里的其它文件暴露出去。
func (s *Server) brandingFile(c *gin.Context) {
	name := c.Param("name")
	_, logo, icon, dir := s.brandingSnapshot()
	if name == "" || (name != logo && name != icon) {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}
	contentType, ok := brandingContentTypes[filepath.Ext(name)]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
			return
		}
		serverError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age="+strconv.Itoa(brandingMaxAgeSeconds))
	c.Data(http.StatusOK, contentType, body)
}

// readBrandingFile 读取上传内容，超限即拒绝（含 Size 字段造假的情况）。
func readBrandingFile(f *multipart.FileHeader, limit int) ([]byte, error) {
	if f.Size > int64(limit) {
		return nil, fmt.Errorf("图片超过 %d KB，请压缩后再上传", limit>>10)
	}
	opened, err := f.Open()
	if err != nil {
		return nil, errors.New("读取上传文件失败")
	}
	defer opened.Close()
	data, err := io.ReadAll(io.LimitReader(opened, int64(limit)+1))
	if err != nil {
		return nil, errors.New("读取上传文件失败")
	}
	if len(data) > limit {
		return nil, fmt.Errorf("图片超过 %d KB，请压缩后再上传", limit>>10)
	}
	if len(data) == 0 {
		return nil, errors.New("图片内容为空")
	}
	return data, nil
}

// brandingExt 用内容嗅探判定真实格式，返回落盘用的扩展名。
// 不信任客户端声明的 filename / Content-Type，避免任意内容落到同源路径上。
func brandingExt(data []byte) (string, bool) {
	mimeType := http.DetectContentType(data)
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = strings.TrimSpace(mimeType[:i])
	}
	ext, ok := brandingImageTypes[mimeType]
	return ext, ok
}

// writeBrandingFile 先写临时文件再改名，避免同名覆盖失败时留下半张图。
func writeBrandingFile(dir, name string, data []byte) error {
	dst := filepath.Join(dir, name)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, brandingFileMode); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		if rmErr := os.Remove(tmp); rmErr != nil {
			slog.Warn("清理临时素材失败", "path", tmp, "err", rmErr)
		}
		return err
	}
	return nil
}

// removeBrandingFile 删除被替换掉的旧素材；文件本来就不存在不算异常。
func removeBrandingFile(dir, name string) {
	path := filepath.Join(dir, name)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("删除旧站点素材失败", "path", path, "err", err)
	}
}

// saveBrandingConfig 在请求里带上 save_to_file 时把配置写回文件。
// 表单与查询参数都要支持：上传走 multipart，清除走 query。
func saveBrandingConfig(c *gin.Context, configPath string, cfg *config.Config) error {
	if !brandingPersist(c) || configPath == "" {
		return nil
	}
	return config.Save(configPath, cfg)
}

func brandingPersist(c *gin.Context) bool {
	value := strings.TrimSpace(c.PostForm("save_to_file"))
	if value == "" {
		value = strings.TrimSpace(c.Query("save_to_file"))
	}
	switch strings.ToLower(value) {
	case "true", "1", "on":
		return true
	default:
		return false
	}
}
