// Package webrelay 实现「网页会话中继」账号池与网页协议客户端。
//
// 当前支持两个文本模型站点：
//   - deepseek：chat.deepseek.com（userToken 鉴权 + PoW 挑战）
//   - qwen：chat.qwen.ai（网页 token 鉴权）
//
// 账号池语义与豆包账号池保持一致：
//   - 文本生成从账号池取号，单账号被风控/限流时自动冷却并切换下一个账号；
//   - 登录态失效（token 过期）区别于风控限流，不进冷却，需重新导入；
//   - 只接受「手动粘贴 token/Cookie」来源。
package webrelay

import (
	"infinite-canvas/backend/internal/netproxy"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// 站点标识。
const (
	SiteDeepSeek = "deepseek"
	SiteQwen     = "qwen"
)

// 失败分类（与豆包账号池语义对齐）。
const (
	FailKindRateLimited    = "rate_limited"
	FailKindQuotaExhausted = "quota_exhausted"
	FailKindSessionExpired = "session_expired"
	// FailKindInvalidInput 上游判定输入/附件本身无效（如视频过短），与账号状态无关。
	FailKindInvalidInput = "invalid_input"
)

const (
	// CooldownDefaultMs 普通失败（风控/限流）的默认冷却时长。
	CooldownDefaultMs int64 = 60 * 1000
	// CooldownQuotaMs 额度耗尽的冷却时长（30 分钟）。
	CooldownQuotaMs int64 = 30 * 60 * 1000
)

// NormalizeSite 校验站点标识。
func NormalizeSite(site string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(site)) {
	case SiteDeepSeek:
		return SiteDeepSeek, nil
	case SiteQwen:
		return SiteQwen, nil
	default:
		return "", fmt.Errorf("不支持的中继站点：%s（仅支持 deepseek / qwen）", site)
	}
}

// NormalizeToken 从粘贴文本中提取真正的凭据 token。
// 千问支持粘贴整段 Cookie（提取 token=）、Bearer 前缀或裸 token；DeepSeek 是裸 userToken。
func NormalizeToken(site string, raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	text = strings.TrimPrefix(text, "Bearer ")
	// DeepSeek 新版网页把 userToken 存成 JSON 包装（形如 {"value":"<token>",...:"0"}），
	// 一键导入/手动粘贴都可能带进整个 JSON 体——识别并解出内层 value。
	if strings.HasPrefix(text, "{") {
		var wrapper map[string]any
		if err := json.Unmarshal([]byte(text), &wrapper); err == nil {
			if v, ok := wrapper["value"].(string); ok && strings.TrimSpace(v) != "" {
				text = strings.TrimSpace(v)
			}
		}
	}
	if site == SiteQwen {
		// 从 Cookie 串中提取 token= 值。
		for _, part := range strings.Split(text, ";") {
			part = strings.TrimSpace(part)
			if idx := strings.Index(part, "="); idx > 0 && strings.EqualFold(strings.TrimSpace(part[:idx]), "token") {
				return strings.TrimSpace(part[idx+1:])
			}
		}
	}
	return text
}

// NormalizeCookieHeader 保留整段 Cookie（千问可选增强）；DeepSeek 不需要。
func NormalizeCookieHeader(site string, raw string, token string) string {
	if site != SiteQwen {
		return ""
	}
	text := strings.TrimSpace(raw)
	if text == "" || !strings.Contains(text, "=") {
		return ""
	}
	// 避免把裸 token 误当 Cookie 存进去。
	if strings.TrimSpace(token) != "" && text == strings.TrimSpace(token) {
		return ""
	}
	return text
}

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// MaskToken 脱敏展示。
func MaskToken(site string, token string) string {
	id := NormalizeToken(site, token)
	if id == "" {
		return "(empty)"
	}
	if strings.Contains(id, ".") {
		// JWT 形态（千问）：展示头两段长度特征即可。
		parts := strings.SplitN(id, ".", 3)
		head := parts[0]
		if len(head) > 8 {
			head = head[:8]
		}
		return head + "...***"
	}
	if len(id) < 12 {
		return "***"
	}
	return id[:6] + "..." + id[len(id)-4:]
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// Credential 取号结果（调用方拿去发请求）。
type Credential struct {
	AccountID string
	Site      string
	Token     string
	Cookie    string
	// ProxyURL 账号绑定的出网代理地址（取号时从 network_proxies 解析，空 = 直连）。
	ProxyURL  string
}

// AccountView 账号脱敏视图（对外 JSON）。
type AccountView struct {
	ID             string     `json:"id"`
	Site           string     `json:"site"`
	Label          string     `json:"label"`
	Masked         string     `json:"masked"`
	State          string     `json:"state"`
	StatusText     string     `json:"statusText"`
	Enabled        bool       `json:"enabled"`
	LoginExpired   bool       `json:"loginExpired"`
	CooldownUntil  *time.Time `json:"cooldownUntil"`
	LastError      string     `json:"lastError"`
	SuccessCount   int        `json:"successCount"`
	UseCount       int        `json:"useCount"`
	FailCount      int        `json:"failCount"`
	LastUsedAt     *time.Time `json:"lastUsedAt"`
	Note           string     `json:"note"`
	Source         string     `json:"source"`
	ProxyID        string     `json:"proxyId"`
	HasCookie      bool       `json:"hasCookie"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// PoolStatus 单站点账号池状态。
type PoolStatus struct {
	Site           string        `json:"site"`
	AccountCount   int           `json:"accountCount"`
	AvailableCount int           `json:"availableCount"`
	CoolingCount   int           `json:"coolingCount"`
	ExpiredCount   int           `json:"expiredCount"`
	DisabledCount  int           `json:"disabledCount"`
	TotalSuccess   int           `json:"totalSuccess"`
	TotalFail      int           `json:"totalFail"`
	Accounts       []AccountView `json:"accounts"`
	CheckedAt      time.Time     `json:"checkedAt"`
}

// UpsertInput 新增/更新账号。
type UpsertInput struct {
	Site   string
	Token  string
	Cookie string
	Label  string
	Note   string
	Source string
}

// UpdatePatch 编辑账号。
type UpdatePatch struct {
	Label   *string
	Note    *string
	Enabled *bool
	ProxyID *string
}

// MarkFailedOptions 生成上游的三类失败，供取号方决定冷却策略。
type MarkFailedOptions struct {
	Kind       string
	Message    string
	CooldownMs int64
}

// ErrorKind 从错误中识别失败分类（客户端错误实现 ErrorKindProvider）。
type ErrorKindProvider interface{ RelayErrorKind() string }

// ErrorKind 提取错误的失败分类；无法识别时返回空串。
func ErrorKind(err error) string {
	var provider ErrorKindProvider
	if errors.As(err, &provider) {
		return provider.RelayErrorKind()
	}
	return ""
}

// Service 网页中继账号池。
type Service struct {
	mu sync.Mutex
	db *gorm.DB
}

// NewService 创建账号池服务（db 可为 nil，仅内存态）。
func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) dbOrErr() (*gorm.DB, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("账号池存储不可用")
	}
	return s.db, nil
}

// PruneMalformedTokens 清掉某站点里明显不是合法凭据的账号
// （典型：把 DeepSeek 网页的 JSON 包装体 {"value":...} 整个当成 token 存进来的旧数据）。
func (s *Service) PruneMalformedTokens(site string) (int64, error) {
	db, err := s.dbOrErr()
	if err != nil {
		return 0, err
	}
	res := db.WithContext(context.Background()).
		Where("site = ? AND (token LIKE ? OR token = '')", site, "{%").
		Delete(&model.WebRelayAccount{})
	return res.RowsAffected, res.Error
}

// Upsert 新增或更新账号（site+token 唯一）。
func (s *Service) Upsert(input UpsertInput) (*AccountView, error) {
	site, err := NormalizeSite(input.Site)
	if err != nil {
		return nil, err
	}
	token := NormalizeToken(site, input.Token)
	if token == "" {
		return nil, errors.New("token 不能为空")
	}
	if len(token) < 16 {
		return nil, errors.New("token 长度无效，请确认粘贴的是完整登录凭据")
	}
	cookie := NormalizeCookieHeader(site, input.Cookie, token)
	label := strings.TrimSpace(input.Label)
	note := strings.TrimSpace(input.Note)
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "manual"
	}
	db, err := s.dbOrErr()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var account model.WebRelayAccount
	result := db.Where("site = ? AND token = ?", site, token).First(&account)
	if result.Error == nil {
		updates := map[string]any{"updated_at": time.Now()}
		if label != "" {
			updates["label"] = label
		}
		if note != "" {
			updates["note"] = note
		}
		if cookie != "" {
			updates["cookie_header"] = cookie
		}
		// 重新导入视为登录态恢复。
		updates["login_expired"] = false
		updates["last_error"] = ""
		if err := db.Model(&account).Updates(updates).Error; err != nil {
			return nil, err
		}
	} else {
		if label == "" {
			label = defaultAccountLabel(site)
		}
		account = model.WebRelayAccount{
			ID: newID(), Site: site, Token: token, CookieHeader: cookie,
			Label: label, Note: note, Source: source, Enabled: true,
		}
		if err := db.Create(&account).Error; err != nil {
			return nil, err
		}
	}
	view := accountView(account)
	return &view, nil
}

// HasToken 判断某站点是否已存在该 token（一键导入的去重提示用）。
func (s *Service) HasToken(site string, token string) (bool, error) {
	normalized, err := NormalizeSite(site)
	if err != nil {
		return false, err
	}
	t := strings.TrimSpace(token)
	if t == "" {
		return false, nil
	}
	db, err := s.dbOrErr()
	if err != nil {
		return false, err
	}
	var count int64
	if err := db.Model(&model.WebRelayAccount{}).Where("site = ? AND token = ?", normalized, t).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func defaultAccountLabel(site string) string {
	if site == SiteQwen {
		return "千问账号"
	}
	return "DeepSeek 账号"
}

// Status 单站点账号池状态。
func (s *Service) Status(site string) (*PoolStatus, error) {
	site, err := NormalizeSite(site)
	if err != nil {
		return nil, err
	}
	db, err := s.dbOrErr()
	if err != nil {
		return nil, err
	}
	var accounts []model.WebRelayAccount
	if err := db.Where("site = ?", site).Order("created_at ASC").Find(&accounts).Error; err != nil {
		return nil, err
	}
	status := &PoolStatus{Site: site, Accounts: make([]AccountView, 0, len(accounts)), CheckedAt: time.Now()}
	now := time.Now()
	for _, account := range accounts {
		view := accountView(account)
		status.Accounts = append(status.Accounts, view)
		status.AccountCount++
		status.TotalSuccess += account.SuccessCount
		status.TotalFail += account.FailCount
		switch view.State {
		case "ready":
			status.AvailableCount++
		case "cooling":
			status.CoolingCount++
		case "expired":
			status.ExpiredCount++
		case "disabled":
			status.DisabledCount++
		}
	}
	_ = now
	return status, nil
}

// accountState 计算账号有效状态。
func accountState(account model.WebRelayAccount) (string, string) {
	if !account.Enabled {
		return "disabled", "已停用"
	}
	if account.LoginExpired {
		return "expired", "登录失效"
	}
	if account.CooldownUntil != nil && account.CooldownUntil.After(time.Now()) {
		return "cooling", "冷却中"
	}
	return "ready", "可用"
}

func accountView(account model.WebRelayAccount) AccountView {
	state, statusText := accountState(account)
	return AccountView{
		ID: account.ID, Site: account.Site, Label: account.Label,
		Masked: MaskToken(account.Site, account.Token), State: state, StatusText: statusText,
		Enabled: account.Enabled, LoginExpired: account.LoginExpired,
		CooldownUntil: account.CooldownUntil, LastError: account.LastError,
		SuccessCount: account.SuccessCount, UseCount: account.UseCount, FailCount: account.FailCount,
		LastUsedAt: account.LastUsedAt, Note: account.Note, Source: account.Source,
		ProxyID: account.ProxyID,
		HasCookie: strings.TrimSpace(account.CookieHeader) != "",
		CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt,
	}
}

// Pick 取号：优先最久未用的可用账号。
func (s *Service) Pick(site string) (*Credential, error) {
	site, err := NormalizeSite(site)
	if err != nil {
		return nil, err
	}
	db, err := s.dbOrErr()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var account model.WebRelayAccount
	result := db.
		Where("site = ? AND enabled = ? AND login_expired = ? AND (cooldown_until IS NULL OR cooldown_until <= ?)", site, true, false, now).
		Order("last_used_at ASC NULLS FIRST").First(&account)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// NULLS FIRST 在部分 SQLite 版本不可用，回退到应用层挑选。
			return s.pickFallback(site, now)
		}
		return nil, result.Error
	}
	return s.markPicked(account), nil
}

func (s *Service) pickFallback(site string, now time.Time) (*Credential, error) {
	var accounts []model.WebRelayAccount
	if err := db_Where(s, site, &accounts); err != nil {
		return nil, err
	}
	var chosen *model.WebRelayAccount
	for i := range accounts {
		account := accounts[i]
		if !account.Enabled || account.LoginExpired {
			continue
		}
		if account.CooldownUntil != nil && account.CooldownUntil.After(now) {
			continue
		}
		if chosen == nil || (account.LastUsedAt != nil && (chosen.LastUsedAt == nil || account.LastUsedAt.Before(*chosen.LastUsedAt))) {
			copied := account
			chosen = &copied
		}
		if chosen != nil && chosen.LastUsedAt == nil {
			break
		}
	}
	if chosen == nil {
		return nil, errors.New("账号池没有可用账号，请先导入账号或解除冷却")
	}
	return s.markPicked(*chosen), nil
}

func db_Where(s *Service, site string, out *[]model.WebRelayAccount) error {
	db, err := s.dbOrErr()
	if err != nil {
		return err
	}
	return db.Where("site = ?", site).Find(out).Error
}

func (s *Service) markPicked(account model.WebRelayAccount) *Credential {
	now := time.Now()
	if s.db != nil {
		_ = s.db.Model(&model.WebRelayAccount{}).Where("id = ?", account.ID).
			Updates(map[string]any{"use_count": account.UseCount + 1, "last_used_at": now}).Error
	}
	return &Credential{AccountID: account.ID, Site: account.Site, Token: account.Token, Cookie: account.CookieHeader, ProxyURL: netproxy.LookupURL(s.db, account.ProxyID)}
}

// MarkSuccess 记录成功。
func (s *Service) MarkSuccess(id string) error {
	db, err := s.dbOrErr()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return db.Model(&model.WebRelayAccount{}).Where("id = ?", id).Updates(map[string]any{
		"success_count": gorm.Expr("success_count + 1"),
		"last_error":    "",
	}).Error
}

// MarkFailed 记录失败并按类别冷却；返回是否影响了账号。
func (s *Service) MarkFailed(id string, opts MarkFailedOptions) error {
	db, err := s.dbOrErr()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updates := map[string]any{
		"fail_count": gorm.Expr("fail_count + 1"),
		"last_error": truncate(strings.TrimSpace(opts.Message), 480),
	}
	switch opts.Kind {
	case FailKindSessionExpired:
		updates["login_expired"] = true
	case FailKindQuotaExhausted:
		cooldown := opts.CooldownMs
		if cooldown <= 0 {
			cooldown = CooldownQuotaMs
		}
		until := time.Now().Add(time.Duration(cooldown) * time.Millisecond)
		updates["cooldown_until"] = &until
	case FailKindRateLimited:
		cooldown := opts.CooldownMs
		if cooldown <= 0 {
			cooldown = CooldownDefaultMs
		}
		until := time.Now().Add(time.Duration(cooldown) * time.Millisecond)
		updates["cooldown_until"] = &until
	}
	return db.Model(&model.WebRelayAccount{}).Where("id = ?", id).Updates(updates).Error
}

// Update 编辑账号。
func (s *Service) Update(id string, patch UpdatePatch) (*AccountView, error) {
	db, err := s.dbOrErr()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var account model.WebRelayAccount
	if err := db.Where("id = ?", id).First(&account).Error; err != nil {
		return nil, errors.New("账号不存在")
	}
	updates := map[string]any{"updated_at": time.Now()}
	if patch.Label != nil {
		updates["label"] = strings.TrimSpace(*patch.Label)
	}
	if patch.Note != nil {
		updates["note"] = strings.TrimSpace(*patch.Note)
	}
	if patch.Enabled != nil {
		updates["enabled"] = *patch.Enabled
	}
	if patch.ProxyID != nil {
		updates["proxy_id"] = strings.TrimSpace(*patch.ProxyID)
	}
	if err := db.Model(&account).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := db.Where("id = ?", id).First(&account).Error; err != nil {
		return nil, err
	}
	view := accountView(account)
	return &view, nil
}

// Remove 删除账号。
func (s *Service) Remove(id string) error {
	db, err := s.dbOrErr()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return db.Where("id = ?", id).Delete(&model.WebRelayAccount{}).Error
}

// BatchOp 批量操作：enable / disable / remove / clear-cooldown。
func (s *Service) BatchOp(action string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db, err := s.dbOrErr()
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var affected int64
	switch strings.TrimSpace(action) {
	case "enable":
		affected = db.Model(&model.WebRelayAccount{}).Where("id IN ?", ids).Updates(map[string]any{"enabled": true}).RowsAffected
	case "disable":
		affected = db.Model(&model.WebRelayAccount{}).Where("id IN ?", ids).Updates(map[string]any{"enabled": false}).RowsAffected
	case "remove":
		affected = db.Where("id IN ?", ids).Delete(&model.WebRelayAccount{}).RowsAffected
	case "clear-cooldown":
		affected = db.Model(&model.WebRelayAccount{}).Where("id IN ?", ids).Updates(map[string]any{"cooldown_until": nil}).RowsAffected
	default:
		return 0, fmt.Errorf("不支持的批量操作：%s", action)
	}
	return int(affected), nil
}

// ClearAllCooldowns 清空站点全部冷却。
func (s *Service) ClearAllCooldowns(site string) (int, error) {
	site, err := NormalizeSite(site)
	if err != nil {
		return 0, err
	}
	db, err := s.dbOrErr()
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	affected := db.Model(&model.WebRelayAccount{}).Where("site = ?", site).Updates(map[string]any{"cooldown_until": nil}).RowsAffected
	return int(affected), nil
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
