//go:build webui

// Package webui 以 embed 方式托管前端构建产物（web/dist）。
// 仅在 `go build -tags webui` 且 dist 目录存在时参与编译；
// 服务端普通构建走 stub.go，不携带前端资源。
package webui

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:dist
var distFS embed.FS

// Dist 返回前端静态资源根（dist/ 子目录）。
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// HasDist 报告是否打包了前端资源。
func HasDist() bool {
	_, err := Dist()
	return err == nil
}

// SPAHandler 返回单页应用处理器：
//   - /api、/oauth 开头的未匹配路径交给 apiFallback（系统渠道模型代理等后端兜底）；
//   - 其余路径优先命中静态文件，未命中时回退 index.html 交给前端路由。
func SPAHandler(dist fs.FS, apiFallback gin.HandlerFunc) gin.HandlerFunc {
	fileServer := &spaFileServer{root: dist}
	return func(c *gin.Context) {
		if (strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/oauth/")) && apiFallback != nil {
			apiFallback(c)
			return
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}

// spaFileServer 提供静态文件服务并在未命中时回退 index.html。
// 注意：dist 里有 canvas/、assets/ 等静态目录与前端路由同名，
// 目录请求必须回退 index.html，绝不能交给 FileServer 做目录重定向/列表。
type spaFileServer struct {
	root fs.FS
}

func (s *spaFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	serveIndex := true
	if info, err := fs.Stat(s.root, name); err == nil && !info.IsDir() {
		serveIndex = false
	}
	if serveIndex {
		// 直接读 index.html 返回；不能交给 http.FileServer，
		// 它会把对 /index.html 的请求 301 到 "./"，破坏 SPA 深链刷新。
		// 另外必须显式 WriteHeader(200)：gin 进入 NoRoute 前已预设 404 状态。
		data, err := fs.ReadFile(s.root, "index.html")
		if err != nil {
			log.Printf("webui: index.html missing: %v", err)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	http.FileServerFS(s.root).ServeHTTP(w, r)
}
