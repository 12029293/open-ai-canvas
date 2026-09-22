package handler

// 网页中继账号池 REST API（DeepSeek 网页版 / 千问网页版）。

import (
	"net/http"

	"infinite-canvas/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterWebRelayAccountRoutes 注册 /webrelay-accounts 路由（需登录）。
func RegisterWebRelayAccountRoutes(api *gin.RouterGroup, svc *service.Service) {
	group := api.Group("/webrelay-accounts")
	group.Use(func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "未登录", "reason": "unauthorized"})
			return
		}
		c.Set("currentUser", user)
		c.Next()
	})

	group.POST("/import-browser", func(c *gin.Context) {
		var req service.WebRelayBrowserImportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.WebRelayImportFromBrowser(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, result)
	})

	// 状态查询：GET /webrelay-accounts?site=deepseek|qwen
	group.GET("", func(c *gin.Context) {
		status, err := svc.WebRelayPoolStatus(c.Query("site"))
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, status)
	})

	group.POST("", func(c *gin.Context) {
		var req service.WebRelayUpsertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		view, err := svc.WebRelayUpsertAccount(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"account": view})
	})

	group.PATCH("/:id", func(c *gin.Context) {
		var req service.WebRelayUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		view, err := svc.WebRelayUpdateAccount(c.Param("id"), req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"account": view})
	})

	group.DELETE("/:id", func(c *gin.Context) {
		if err := svc.WebRelayRemoveAccount(c.Param("id")); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})

	group.POST("/batch", func(c *gin.Context) {
		var req service.WebRelayBatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		affected, err := svc.WebRelayBatchOp(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"affected": affected})
	})

	// 连接测试：POST /webrelay-accounts/test {interfaceType, tokens, model}
	group.POST("/test", func(c *gin.Context) {
		var req service.WebRelayTestRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.WebRelayTestConnection(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, result)
	})

	group.POST("/clear-cooldowns", func(c *gin.Context) {
		cleared, err := svc.WebRelayClearAllCooldowns(c.Query("site"))
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"cleared": cleared})
	})
}
