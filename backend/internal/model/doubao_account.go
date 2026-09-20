package model

import "time"

// DoubaoAccount 豆包账号池账号。
// Cookie 等敏感字段不序列化到 JSON，对外只暴露脱敏视图（见 doubao 域的 AccountView）。
type DoubaoAccount struct {
	ID        string `gorm:"size:64;primaryKey" json:"id"`
	// Site 站点标识：doubao | dola | jimeng（同一账号池表承载多站点账号）。
	Site    string `gorm:"size:16;not null;default:'doubao';index:idx_doubao_accounts_site_session,priority:1" json:"site"`
	Label   string `gorm:"size:120;not null;default:''" json:"label"`
	// SessionID 从 Cookie 提取的会话标识；唯一性按 (site, session_id) 组合保证
	// （历史全局唯一索引由 V22 迁移重建）。
	SessionID string `gorm:"size:512;not null;index:idx_doubao_accounts_site_session,priority:2,unique" json:"-"`
	CookieHeader     string     `gorm:"type:text" json:"-"`
	Source           string     `gorm:"size:32;not null;default:'manual'" json:"source"`
	Enabled          bool       `gorm:"not null;default:true" json:"enabled"`
	LoginExpired     bool       `gorm:"not null;default:false" json:"loginExpired"`
	CooldownUntil    *time.Time `json:"cooldownUntil"`
	QuotaExhaustedAt *time.Time `json:"quotaExhaustedAt"`
	LastError        string     `gorm:"size:512;not null;default:''" json:"lastError"`
	SuccessCount     int        `gorm:"not null;default:0" json:"successCount"`
	UseCount         int        `gorm:"not null;default:0" json:"useCount"`
	FailCount        int        `gorm:"not null;default:0" json:"failCount"`
	LastUsedAt       *time.Time `json:"lastUsedAt"`
	// Dola 站点每日视频配额：video_quota_date 记录计数归属日（YYYY-MM-DD），
	// 不是当天则计数视为 0（跨天自动恢复）；豆包/即梦账号不使用该配额。
	VideoCountUsed int    `gorm:"not null;default:0" json:"videoCountUsed"`
	VideoQuotaDate string `gorm:"size:8;not null;default:''" json:"videoQuotaDate"`
	// ProxyID 绑定的网络代理 ID（空 = 直连）；ProxyURL 冗余存储解析后的代理地址，
	// 代理被删除时随解绑一起清空。
	ProxyID   string `gorm:"size:64;not null;default:''" json:"proxyId"`
	ProxyURL  string `gorm:"size:512;not null;default:''" json:"-"`
	// Tags 以逗号连接存储（单账号最多 8 个、每个 16 字符，见 doubao.NormalizeTags）。
	Tags      string    `gorm:"size:256;not null;default:''" json:"tags"`
	Note      string    `gorm:"size:256;not null;default:''" json:"note"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (DoubaoAccount) TableName() string { return "doubao_accounts" }
