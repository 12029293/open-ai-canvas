package netproxy

// 代理配置的跨域读取工具：豆包池 / 网页中继池在取号时解析账号绑定的代理地址。
// 独立成包是为了避免 doubao / webrelay 互相依赖。

import (
	"strings"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// LookupURL 返回账号绑定的代理地址；未绑定、库不可用或配置已删除时返回空串（直连）。
// 动态轮换代理（用户名含 {sid} 占位符）每次调用渲染新的随机会话标识，
// 因此应在每次取号时调用，保证每次提交任务拿到新出口 IP。
func LookupURL(db *gorm.DB, proxyID string) string {
	proxyID = strings.TrimSpace(proxyID)
	if db == nil || proxyID == "" {
		return ""
	}
	var proxy model.NetworkProxy
	if err := db.Where("id = ?", proxyID).First(&proxy).Error; err != nil {
		return ""
	}
	return proxy.ProxyURL()
}

// LookupURLStable 与 LookupURL 一致，但动态代理按 seed（账号 ID）派生固定 sid：
// 同一账号每次取号都落在同一出口会话上（账号级固定 IP，IP 轨迹稳定），
// 避免同一账号频繁更换出口 IP 触发上游风控。静态代理行为与 LookupURL 相同。
func LookupURLStable(db *gorm.DB, proxyID, seed string) string {
	proxyID = strings.TrimSpace(proxyID)
	if db == nil || proxyID == "" {
		return ""
	}
	var proxy model.NetworkProxy
	if err := db.Where("id = ?", proxyID).First(&proxy).Error; err != nil {
		return ""
	}
	return proxy.RenderStableProxyURL(seed)
}
