//go:build !webui

// 未启用 webui 构建标签时的占位实现：不携带前端资源，保持普通构建零成本。
package webui

import (
	"errors"
	"io/fs"

	"github.com/gin-gonic/gin"
)

// Dist 返回错误：需要 `go build -tags webui` 并先构建前端产物。
func Dist() (fs.FS, error) {
	return nil, errors.New("webui 未打包：请先构建 web/dist 并使用 -tags webui 编译")
}

// HasDist 恒为 false。
func HasDist() bool { return false }

// SPAHandler 未打包前端时直接走原有 API 兜底。
func SPAHandler(dist fs.FS, apiFallback gin.HandlerFunc) gin.HandlerFunc {
	if apiFallback != nil {
		return apiFallback
	}
	return func(c *gin.Context) { c.AbortWithStatus(404) }
}
