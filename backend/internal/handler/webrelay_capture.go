package handler

// 网页版凭据「一键导入」公开接口：书签脚本在网页版页面上运行并上报凭据。
// 该接口无鉴权：服务只监听 127.0.0.1，凭据由上报方自己持有，被滥用最多写入垃圾凭据。
// 带 CORS 头以便 https 网页版域名下的书签脚本调用（浏览器把 127.0.0.1 视为可信回环地址）。

import (
	"net/http"

	"infinite-canvas/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func RegisterWebRelayCaptureRoutes(api *gin.RouterGroup, svc *service.Service) {
	cors := func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
	}
	api.OPTIONS("/webrelay-capture", func(c *gin.Context) {
		cors(c)
		c.Status(http.StatusNoContent)
	})
	api.POST("/webrelay-capture", func(c *gin.Context) {
		cors(c)
		var req service.WebRelayCaptureRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.WebRelayCaptureCredential(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, result)
	})
}
