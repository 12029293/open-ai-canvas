package model

import "time"

// WebRelayAccount 网页中继账号池账号（DeepSeek 网页版 / 千问网页版）。
// 凭据敏感字段不序列化到 JSON，对外只暴露脱敏视图（见 webrelay 域 AccountView）。
type WebRelayAccount struct {
	ID    string `gorm:"size:64;primaryKey" json:"id"`
	Site  string `gorm:"size:24;not null;uniqueIndex:idx_webrelay_site_token" json:"site"`
	Token string `gorm:"size:1024;not null;uniqueIndex:idx_webrelay_site_token" json:"-"`
	// CookieHeader 仅千问使用：网页登录态除了 token 外附带的整段 Cookie（可选）。
	CookieHeader string `gorm:"type:text" json:"-"`
	Label        string `gorm:"size:120;not null;default:''" json:"label"`
	Source       string `gorm:"size:32;not null;default:'manual'" json:"source"`
	Enabled      bool   `gorm:"not null;default:true" json:"enabled"`
	LoginExpired bool   `gorm:"not null;default:false" json:"loginExpired"`
	// CooldownUntil 风控/限流冷却；LoginExpired 属于登录态失效，不进冷却，需重新导入。
	CooldownUntil *time.Time `json:"cooldownUntil"`
	LastError     string     `gorm:"size:512;not null;default:''" json:"lastError"`
	SuccessCount  int        `gorm:"not null;default:0" json:"successCount"`
	UseCount      int        `gorm:"not null;default:0" json:"useCount"`
	FailCount     int        `gorm:"not null;default:0" json:"failCount"`
	LastUsedAt    *time.Time `json:"lastUsedAt"`
	Note          string     `gorm:"size:256;not null;default:''" json:"note"`
	// ProxyID 绑定的出网代理（network_proxies.id，空 = 直连）。
	ProxyID       string     `gorm:"size:64;not null;default:''" json:"proxyId"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func (WebRelayAccount) TableName() string { return "web_relay_accounts" }
