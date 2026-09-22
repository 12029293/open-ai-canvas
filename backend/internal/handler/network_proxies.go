package handler

// 网络代理 REST API（账号池出站代理）。

import (
	"net/http"

	"infinite-canvas/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterNetworkProxyRoutes 注册 /network-proxies 路由（需登录）。
func RegisterNetworkProxyRoutes(api *gin.RouterGroup, svc *service.Service) {
	group := api.Group("/network-proxies")
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
		views, err := svc.NetworkProxyList()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		ok(c, gin.H{"proxies": views})
	})

	group.POST("", func(c *gin.Context) {
		var req service.NetworkProxyUpsertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		view, err := svc.NetworkProxyCreate(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"proxy": view})
	})

	group.PATCH("/:id", func(c *gin.Context) {
		var req service.NetworkProxyUpsertRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		view, err := svc.NetworkProxyUpdate(c.Param("id"), req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"proxy": view})
	})

	group.DELETE("/:id", func(c *gin.Context) {
		if err := svc.NetworkProxyDelete(c.Param("id")); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})

	group.POST("/:id/test", func(c *gin.Context) {
		result, err := svc.NetworkProxyTest(c.Param("id"))
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"result": result})
	})

	group.POST("/assign", func(c *gin.Context) {
		var req service.NetworkProxyAssignRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		affected, err := svc.NetworkProxyAssign(req)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		ok(c, gin.H{"affected": affected})
	})
}
