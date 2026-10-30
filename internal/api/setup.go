package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/auth"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
)

// setupStatus 返回系统安装状态
func (s *Server) setupStatus(c *gin.Context) {
	n, err := s.store.CountUsers()
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"installed": n > 0,
	})
}

type setupRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	TMDBAPIKey  string `json:"tmdb_api_key"`
	// PublicURL 是用户实际访问站点的根地址。反代/隧道会改写 Host，留空则同源校验与
	// sitemap 都会以内网地址为准，所以向导里默认带上浏览器正在用的地址。
	PublicURL string `json:"public_url"`
}

// setup 完成初始安装，创建管理员账号
func (s *Server) setup(c *gin.Context) {
	// 检查是否已安装
	n, err := s.store.CountUsers()
	if err != nil {
		serverError(c, err)
		return
	}
	if n > 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "系统已安装，请勿重复操作"})
		return
	}

	var req setupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if !usernameRe.MatchString(req.Username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名 3-32 位，仅限字母、数字、下划线、点和短横线"})
		return
	}
	if !validPassword(req.Password) {
		c.JSON(http.StatusBadRequest, gin.H{"error": passwordHintFor(req.Password)})
		return
	}
	if len(req.DisplayName) > 40 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "显示名过长"})
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = "管理员"
	}
	publicURL, ok := config.NormalizePublicURL(req.PublicURL)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "对外访问地址需要以 http:// 或 https:// 开头"})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}

	u := &model.User{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Role:        model.RoleAdmin,
		Enabled:     true,
	}
	if err := s.store.CreateUser(u, hash); err != nil {
		serverError(c, err)
		return
	}

	slog.Info("安装完成，管理员已创建", "username", u.Username, "public_url", publicURL)

	// TMDB Key 与对外访问地址都写进配置文件，省掉手工编辑
	tmdbKey := strings.TrimSpace(req.TMDBAPIKey)
	if (tmdbKey != "" || publicURL != "") && s.cfg != nil && s.configPath != "" {
		s.configMu.Lock()
		if tmdbKey != "" {
			s.cfg.TMDB.APIKey = tmdbKey
		}
		if publicURL != "" {
			s.cfg.Server.PublicURL = publicURL
		}
		saveErr := config.Save(s.configPath, s.cfg)
		s.configMu.Unlock()
		if saveErr != nil {
			slog.Warn("保存安装配置失败", "err", saveErr)
		} else if s.reload != nil {
			s.reload(s.cfg)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"user_id": u.ID,
	})
}
