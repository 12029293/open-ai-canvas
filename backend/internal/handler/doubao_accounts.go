package handler

// 豆包账号池 REST API。

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterDoubaoAccountRoutes 注册 /doubao-accounts 路由（需登录）。
func RegisterDoubaoAccountRoutes(api *gin.RouterGroup, svc *service.Service) {
	group := api.Group("/doubao-accounts")
	group.Use(func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "未登录", "reason": "unauthorized"})
			return
		}
		c.Set("currentUser", user)
		c.Next()
	})

	group.GET("", func(c *gin.Context) {
		status, err := svc.DoubaoPoolStatus(c.Query("site"))
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		ok(c, status)
	})

	// 扫码登录：启动本机浏览器登录会话（同一站点同一时间仅一个）。
	group.POST("/qr/start", func(c *gin.Context) {
		var req struct {
			Site string `json:"site"`
		}
		_ = c.ShouldBindJSON(&req)
		view, started := svc.DoubaoQrStart(req.Site)
		ok(c, gin.H{"session": view, "started": started})
	})

	group.GET("/qr/status", func(c *gin.Context) {
		ok(c, gin.H{"session": svc.DoubaoQrStatus(c.Query("site"))})
	})

	group.POST("/qr/cancel", func(c *gin.Context) {
		var req struct {
			Site string `json:"site"`
		}
		_ = c.ShouldBindJSON(&req)
		if err := svc.DoubaoQrCancel(req.Site); err != nil {
			fail(c, http.StatusConflict, err)
			return
		}
		ok(c, gin.H{"canceled": true})
	})

	// 手动过验证（710022004 风控）：后端弹出本机浏览器并预注入该账号 Cookie，
	// 用户在窗口里完成滑块/安全验证后点「完成验证」回收最新 Cookie。
	group.POST("/:id/verify/start", func(c *gin.Context) {
		view, started, err := svc.DoubaoVerifyStart(c.Param("id"))
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"session": view, "started": started})
	})

	group.GET("/verify/status", func(c *gin.Context) {
		ok(c, gin.H{"session": svc.DoubaoVerifyStatus()})
	})

	group.POST("/verify/capture", func(c *gin.Context) {
		view, err := svc.DoubaoVerifyCapture()
		if err != nil {
			// 失败时带回当前会话快照，前端据此继续轮询/展示
			c.JSON(http.StatusConflict, gin.H{"code": 409, "msg": err.Error(), "reason": "verify_capture_failed", "session": view})
			return
		}
		ok(c, gin.H{"session": view})
	})

	group.POST("/verify/cancel", func(c *gin.Context) {
		if err := svc.DoubaoVerifyCancel(); err != nil {
			fail(c, http.StatusConflict, err)
			return
		}
		ok(c, gin.H{"canceled": true})
	})

	// 补抓浏览器指纹：用账号已有 Cookie 开窗注入后捕获真实 UA/设备 ID 并落库
	//（同步执行，约 15~40 秒）。用于修复旧账号无指纹导致的上游顶点限流。
	group.POST("/:id/fingerprint/refresh", func(c *gin.Context) {
		view, err := svc.DoubaoRefreshFingerprint(c.Param("id"))
		if err != nil {
			fail(c, http.StatusConflict, err)
			return
		}
		ok(c, gin.H{"account": view})
	})

	group.POST("", func(c *gin.Context) {
		var req service.DoubaoUpsertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(req.Cookie) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "Cookie/sessionid 不能为空", "reason": "invalid_request"})
			return
		}
		view, err := svc.DoubaoUpsertAccount(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"account": view})
	})

	group.POST("/bulk-import", func(c *gin.Context) {
		var req service.DoubaoBulkImportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.DoubaoBulkImport(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		status, err := svc.DoubaoPoolStatus(req.Site)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		ok(c, gin.H{"result": result, "status": status})
	})

	group.POST("/batch", func(c *gin.Context) {
		var req service.DoubaoBatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		affected, err := svc.DoubaoBatchOp(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		status, err := svc.DoubaoPoolStatus(req.Site)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		ok(c, gin.H{"affected": affected, "status": status})
	})

	group.POST("/clear-cooldowns", func(c *gin.Context) {
		cleared, err := svc.DoubaoClearAllCooldowns()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		ok(c, gin.H{"cleared": cleared})
	})

	group.POST("/pick", func(c *gin.Context) {
		var req struct {
			PreferID string `json:"preferId"`
		}
		_ = c.ShouldBindJSON(&req)
		cred, err := svc.DoubaoPickAccount(req.PreferID)
		if err != nil {
			fail(c, http.StatusConflict, err)
			return
		}
		ok(c, gin.H{"account": cred})
	})

	// 文生图（走账号池，账号类失败自动切换下一个账号）。豆包上游最长 5 分钟。
	group.POST("/generate/image", func(c *gin.Context) {
		var req service.DoubaoGenerateImageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.DoubaoGenerateImage(c.Request.Context(), req)
		if err != nil {
			fail(c, http.StatusBadGateway, err)
			return
		}
		ok(c, result)
	})

	// 文生视频（走账号池，同步等待出片，最长约 12 分钟）。
	group.POST("/generate/video", func(c *gin.Context) {
		var req service.DoubaoGenerateVideoRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.DoubaoGenerateVideo(c.Request.Context(), req)
		if err != nil {
			fail(c, http.StatusBadGateway, err)
			return
		}
		ok(c, result)
	})

	group.PATCH("/:id", func(c *gin.Context) {
		var req service.DoubaoUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		view, err := svc.DoubaoUpdateAccount(c.Param("id"), req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"account": view})
	})

	group.DELETE("/:id", func(c *gin.Context) {
		if err := svc.DoubaoRemoveAccount(c.Param("id")); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})

	group.POST("/:id/mark-success", func(c *gin.Context) {
		if err := svc.DoubaoMarkSuccess(c.Param("id")); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})

	group.POST("/:id/mark-failed", func(c *gin.Context) {
		var req service.DoubaoMarkFailedRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		switched, next, err := svc.DoubaoMarkFailed(c.Param("id"), req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"switched": switched, "next": next})
	})
}
