package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var dist embed.FS

// FS 返回前端构建产物。若未构建（dist 中无 index.html）则返回 nil。
func FS() http.FileSystem {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if f, err := sub.Open("index.html"); err != nil {
		return nil
	} else {
		_ = f.Close()
	}
	return http.FS(sub)
}
