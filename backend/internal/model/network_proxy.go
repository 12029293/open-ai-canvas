package model

import (
	"crypto/rand"
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

// ProxySIDPlaceholder 动态轮换会话占位符。动态住宅代理（如 NexIP）通过用户名中的
// 会话标识（sid）决定出口 IP：占位符存在时，每次取号渲染一个新的随机 sid，
// 实现「每次提交任务换一个出口 IP，任务期间保持同一 IP」的粘性轮换。
const ProxySIDPlaceholder = "{sid}"

// NeedsDynamicSID 用户名是否携带动态会话占位符（如 NexIP 粘性账号 user-sid-{sid}）。
func (p *NetworkProxy) NeedsDynamicSID() bool {
	return strings.Contains(p.Username, ProxySIDPlaceholder)
}

// ProxyURL 拼出可注入 http.Transport / socks5 拨号器的代理地址。
// 动态代理（用户名含 {sid}）每次调用生成新的随机 sid，即每次取号拿到新出口 IP；
// 静态代理返回固定地址。
func (p *NetworkProxy) ProxyURL() string {
	if p.NeedsDynamicSID() {
		return p.RenderProxyURL(randomProxySID())
	}
	return p.RenderProxyURL("")
}

// RenderProxyURL 按指定会话标识渲染代理地址；sid 仅在动态代理下生效。
func (p *NetworkProxy) RenderProxyURL(sid string) string {
	host := strings.TrimSpace(p.Host)
	if host == "" || p.Port <= 0 {
		return ""
	}
	username := strings.ReplaceAll(p.Username, ProxySIDPlaceholder, sid)
	cred := ""
	if username != "" {
		cred = urlEscape(username)
		if p.Password != "" {
			cred += ":" + urlEscape(p.Password)
		}
		cred += "@"
	}
	return fmt.Sprintf("%s://%s%s:%d", p.Protocol, cred, host, p.Port)
}

// RenderStableProxyURL 以 seed 派生固定 sid 渲染代理地址：动态代理对同一 seed
// 恒定返回同一出口会话（账号级固定 IP）；静态代理与 RenderProxyURL 一致。
func (p *NetworkProxy) RenderStableProxyURL(seed string) string {
	if p.NeedsDynamicSID() {
		return p.RenderProxyURL(StableProxySID(seed))
	}
	return p.RenderProxyURL("")
}

// randomProxySID 生成 8 位数字会话标识（NexIP 粘性账号 sid 取值范围 1-99999999）。
func randomProxySID() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	n := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
	return fmt.Sprintf("%08d", 1+n%99999999)
}

// StableProxySID 由固定种子派生 8 位数字会话标识（FNV-1a，取值范围同 randomProxySID）。
// 同一种子恒定映射到同一 sid：账号池用账号 ID 作种子，让每个账号长期绑定
// 同一出口会话（IP 轨迹稳定），避免「每次任务换 IP」本身成为风控信号。
func StableProxySID(seed string) string {
	const (
		fnvOffset uint32 = 2166136261
		fnvPrime  uint32 = 16777619
	)
	h := fnvOffset
	for i := 0; i < len(seed); i++ {
		h ^= uint32(seed[i])
		h *= fnvPrime
	}
	return fmt.Sprintf("%08d", 1+h%99999999)
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
