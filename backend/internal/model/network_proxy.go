package model

import (
	"fmt"
	"strings"
	"time"
)

// 网络代理支持的协议（与前端表单选项一致）。
const (
	ProxyProtocolHTTP   = "http"
	ProxyProtocolHTTPS  = "https"
	ProxyProtocolSocks5 = "socks5"
)

// NormalizedProxyProtocol 归一化代理协议；不支持的协议返回空串。
func NormalizedProxyProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case ProxyProtocolHTTP:
		return ProxyProtocolHTTP
	case ProxyProtocolHTTPS:
		return ProxyProtocolHTTPS
	case ProxyProtocolSocks5:
		return ProxyProtocolSocks5
	default:
		return ""
	}
}

// NetworkProxy 出站网络代理。账号池账号可绑定一个代理；
// 删除代理时必须先解绑全部账号（回到直连）。
type NetworkProxy struct {
	ID string `gorm:"size:64;primaryKey" json:"id"`
	// Name 展示名，账号池下拉里以「名称（host:port）」呈现。
	Name     string `gorm:"size:64;not null" json:"name"`
	Protocol string `gorm:"size:16;not null;default:'http'" json:"protocol"`
	Host     string `gorm:"size:255;not null" json:"host"`
	Port     int    `gorm:"not null" json:"port"`
	// Username/Password 可选认证信息。Password 不回显到前端列表。
	Username string `gorm:"size:128;not null;default:''" json:"username,omitempty"`
	Password string `gorm:"size:128;not null;default:''" json:"-"`
	Remark   string `gorm:"size:255;not null;default:''" json:"remark"`
	// HasPassword 仅表示「已设置密码」，用于编辑时判断是否原样保留。
	HasPassword bool      `gorm:"-" json:"hasPassword"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (NetworkProxy) TableName() string { return "network_proxies" }

// ProxyURL 拼出可注入 http.Transport / socks5 拨号器的代理地址。
func (p *NetworkProxy) ProxyURL() string {
	host := strings.TrimSpace(p.Host)
	if host == "" || p.Port <= 0 {
		return ""
	}
	cred := ""
	if p.Username != "" {
		cred = urlEscape(p.Username)
		if p.Password != "" {
			cred += ":" + urlEscape(p.Password)
		}
		cred += "@"
	}
	return fmt.Sprintf("%s://%s%s:%d", p.Protocol, cred, host, p.Port)
}

// DisplayName 代理地址的安全展示形式（不含认证凭据）。
func (p *NetworkProxy) DisplayName() string {
	return fmt.Sprintf("%s://%s:%d", p.Protocol, strings.TrimSpace(p.Host), p.Port)
}

func urlEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteString(fmt.Sprintf("%%%02X", c))
	}
	return b.String()
}
