package doubao

// 补抓指纹：早期扫码登录的账号没有浏览器指纹（当时抓取实现缺失或落在页面
// 跳转窗口里），生成请求只能用派生指纹，新账号容易被上游顶点限流（710022002）。
// 本流程复用手动过验证的浏览器通道——用账号池已有 Cookie 预注入并打开站点，
// 页面就绪后捕获真实 UA 与设备 ID，经 Upsert（同 sessionid 更新）落库，
// 顺带清除冷却与失败标记，用户无需重新扫码。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// fpRefreshTimeout 补抓全程预算（浏览器启动 + 页面加载 + 捕获重试）。
const fpRefreshTimeout = 90 * time.Second

// RefreshFingerprint 为指定账号补抓浏览器指纹。同步执行（约 15~40 秒）；
// 返回更新后的账号视图。同一时间仅允许一个补抓会话。
func (s *Service) RefreshFingerprint(accountID string) (*AccountView, error) {
	s.mu.Lock()
	var account model.DoubaoAccount
	err := s.db.Where("id = ?", accountID).First(&account).Error
	s.mu.Unlock()
	if err != nil {
		return nil, errors.New("账号不存在")
	}
	site := NormalizeSite(account.Site)
	if account.CookieHeader == "" {
		return nil, errors.New("账号没有 Cookie，无法补抓指纹")
	}

	s.fpRefreshMu.Lock()
	defer s.fpRefreshMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), fpRefreshTimeout)
	defer cancel()

	fp, diag := captureFingerprintWithCookies(ctx, site, account.CookieHeader)
	if fp == nil {
		return nil, fmt.Errorf("指纹捕获失败：%s", diag)
	}
	log.Printf("[doubao] 补抓指纹成功 account=%s ua=%.80s web_id=%s device_id=%s", account.ID, fp.UserAgent, fp.WebID, fp.DeviceID)

	// 同 sessionid 更新：保留显示名（Label 传空），顺带清除冷却/失败标记。
	return s.Upsert(UpsertInput{
		Cookie:      account.CookieHeader,
		Site:        site,
		Source:      account.Source,
		SetActive:   false,
		Fingerprint: fp,
	})
}

// captureFingerprintWithCookies 用临时浏览器注入 Cookie 打开站点并捕获指纹。
func captureFingerprintWithCookies(ctx context.Context, site, cookieHeader string) (*AccountFingerprint, string) {
	bin := ""
	if candidates := browserCandidates(); len(candidates) > 0 {
		bin = candidates[0]
	}
	if bin == "" {
		return nil, "本机未找到 Chrome / Edge"
	}
	cfg := siteLogin(site)
	profileDir := filepath.Join(os.TempDir(), fmt.Sprintf("yingce-%s-fp", site), fmt.Sprintf("acct-%d", time.Now().UnixMilli()))
	controlURL, err := launcher.New().
		Bin(bin).
		Headless(false).
		UserDataDir(profileDir).
		Set("--disable-blink-features", "AutomationControlled").
		Set("--window-size", "1280,900").
		Launch()
	if err != nil {
		return nil, "启动浏览器失败: " + err.Error()
	}
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, "连接浏览器失败: " + err.Error()
	}
	defer func() { _ = browser.Close() }()
	go func() {
		<-ctx.Done()
		_ = browser.Close()
	}()

	page, err := browser.Page(proto.TargetCreateTarget{URL: cfg.loginURL})
	if err != nil {
		return nil, "打开页面失败: " + err.Error()
	}
	domain := siteCookieDomain(site)
	params := make([]*proto.NetworkCookieParam, 0, 24)
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		idx := strings.Index(part, "=")
		if idx <= 0 {
			continue
		}
		params = append(params, &proto.NetworkCookieParam{
			Name: part[:idx], Value: part[idx+1:], Domain: domain, Path: "/",
		})
	}
	if len(params) == 0 {
		return nil, "Cookie 解析为空"
	}
	_ = page.SetCookies(params)
	_ = page.Reload()

	uaFallback := ""
	if bver, verr := browser.Version(); verr == nil && bver != nil {
		uaFallback = bver.UserAgent
	}
	return pageFingerprint(page, uaFallback)
}
