package doubao

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"infinite-canvas/backend/internal/model"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// 手动过验证：710022004（shark_admin verify_scene）这类风控只能由该账号在
// 豆包/Dola/即梦网页端人工完成一次滑块/安全验证。这里复用扫码登录的 go-rod
// 通道，但方向相反——先把账号池里该账号的 Cookie 预注入浏览器（打开即是该
// 账号登录态），用户在窗口里过验证后，一键回收最新完整 Cookie 并经 Upsert
// 清除冷却/失败标记，账号即刻恢复可用。

const (
	verifyPollInterval = 1500 * time.Millisecond
	verifyTimeout      = 15 * time.Minute // 给足过滑块 + 重新登录的时间
)

// 手动验证会话状态。
const (
	VerifyIdle     = "idle"
	VerifyWaiting  = "waiting"  // 浏览器已打开，等待用户过验证
	VerifySuccess  = "success"  // 已回收 Cookie 并写入账号池
	VerifyExpired  = "expired"  // 超时未完成
	VerifyCanceled = "canceled" // 用户取消
	VerifyFailed   = "failed"   // 启动浏览器等硬错误
)

// VerifyView 验证会话的对外只读快照。
type VerifyView struct {
	State        string `json:"state"`
	Message      string `json:"message"`
	AccountID    string `json:"accountId,omitempty"`
	AccountLabel string `json:"accountLabel,omitempty"`
	Site         string `json:"site,omitempty"`
	HasBrowser   bool   `json:"hasBrowser"`
	ElapsedText  string `json:"elapsedText"`
}

type verifySession struct {
	mu           sync.Mutex
	state        string
	message      string
	accountID    string
	accountLabel string
	site         string
	loginURL     string
	startedAt    time.Time
	hasBrowser   bool
	page         *rod.Page
	cancel       context.CancelFunc
}

func (s *verifySession) view() VerifyView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return VerifyView{
		State:        s.state,
		Message:      s.message,
		AccountID:    s.accountID,
		AccountLabel: s.accountLabel,
		Site:         s.site,
		HasBrowser:   s.hasBrowser,
		ElapsedText:  formatElapsed(time.Since(s.startedAt)),
	}
}

func (s *verifySession) update(state, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	if message != "" {
		s.message = message
	}
}

func (s *verifySession) setStateMessage(state, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.message = message
}

func (s *verifySession) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == VerifyWaiting
}

// siteCookieDomain 站点对应的 Cookie 作用域。
func siteCookieDomain(site string) string {
	switch NormalizeSite(site) {
	case SiteDola:
		return ".dola.com"
	case SiteJimeng:
		return ".jianying.com"
	default:
		return ".doubao.com"
	}
}

// IsVerifySceneError 判定是否为「需要人工过安全验证」的上游失败：
// 710022004 显式错误码，或原始页面文本命中 verify_scene / 安全验证。
func IsVerifySceneError(ce *ClassifyError) bool {
	if ce == nil {
		return false
	}
	if ce.Code == 710022004 {
		return true
	}
	return strings.Contains(ce.Message, "710022004") ||
		strings.Contains(ce.Message, "verify_scene") ||
		strings.Contains(ce.Message, "安全验证")
}

// StartManualVerify 为指定账号打开验证窗口（预注入该账号 Cookie）。
// 已有进行中的会话时不重复开窗，直接返回现有会话（started=false）。
func (s *Service) StartManualVerify(accountID string) (VerifyView, bool, error) {
	s.verifyMu.Lock()
	if s.verify != nil && s.verify.alive() {
		view := s.verify.view()
		s.verifyMu.Unlock()
		return view, false, nil
	}
	s.verifyMu.Unlock()

	s.mu.Lock()
	var account model.DoubaoAccount
	err := s.db.Where("id = ?", accountID).First(&account).Error
	s.mu.Unlock()
	if err != nil {
		return VerifyView{}, false, errors.New("账号不存在")
	}
	site := NormalizeSite(account.Site)

	ctx, cancel := context.WithCancel(context.Background())
	session := &verifySession{
		state:        VerifyWaiting,
		message:      "正在启动浏览器…",
		accountID:    account.ID,
		accountLabel: account.Label,
		site:         site,
		loginURL:     siteLogin(site).loginURL,
		startedAt:    time.Now(),
		cancel:       cancel,
	}

	s.verifyMu.Lock()
	if s.verify != nil && s.verify.alive() { // 并发 Start 二次校验
		view := s.verify.view()
		s.verifyMu.Unlock()
		cancel()
		return view, false, nil
	}
	s.verify = session
	s.verifyMu.Unlock()

	go s.runManualVerify(ctx, session, account)
	return session.view(), true, nil
}

// ManualVerifyStatus 当前验证会话快照；无会话时返回 idle。
func (s *Service) ManualVerifyStatus() VerifyView {
	s.verifyMu.Lock()
	session := s.verify
	s.verifyMu.Unlock()
	if session == nil {
		return VerifyView{State: VerifyIdle}
	}
	return session.view()
}

// CaptureManualVerify 用户在窗口中完成滑块/验证后调用：回收最新完整 Cookie，
// 经 Upsert 写回账号池（同一 sessionid 视为更新，自动清除冷却与失败标记）。
func (s *Service) CaptureManualVerify() (VerifyView, error) {
	s.verifyMu.Lock()
	session := s.verify
	s.verifyMu.Unlock()
	if session == nil || !session.alive() {
		return VerifyView{State: VerifyIdle}, errors.New("没有进行中的验证会话")
	}
	session.mu.Lock()
	page := session.page
	session.mu.Unlock()
	if page == nil {
		return session.view(), errors.New("浏览器尚未就绪，请稍后再试")
	}
	cookies, err := page.Cookies([]string{session.loginURL})
	if err != nil || len(cookies) == 0 {
		return session.view(), errors.New("读取浏览器 Cookie 失败，请确认验证窗口仍然打开")
	}
	sid := ""
	for _, cookie := range cookies {
		if cookie.Name == "sessionid" && cookie.Value != "" {
			sid = cookie.Value
			break
		}
	}
	if sid == "" {
		return session.view(), errors.New("尚未检测到登录态：请先在窗口中登录该账号并完成验证")
	}
	cookieHeader := buildCookieHeaderFromRod(cookies)
	uaFallback := ""
	if bver, verr := page.Browser().Version(); verr == nil && bver != nil {
		uaFallback = bver.UserAgent
	}
	fp, fpDiag := pageFingerprint(page, uaFallback)
	if fp == nil {
		log.Printf("[doubao] 验证回收指纹未捕获 account=%s reason=%s", session.accountID, fpDiag)
	}
	account, err := s.Upsert(UpsertInput{
		Cookie:      cookieHeader,
		Site:        session.site,
		Source:      "manual",
		SetActive:   true,
		Fingerprint: fp,
	})
	if err != nil {
		return session.view(), fmt.Errorf("保存 Cookie 失败：%w", err)
	}
	session.update(VerifySuccess, fmt.Sprintf("验证完成，已保存完整 Cookie（%d 条）并恢复账号可用：%s", len(cookies), account.Label))
	session.cancel() // 通知 run 循环退出并关闭浏览器（状态已定格为 success）
	return session.view(), nil
}

// CancelManualVerify 取消进行中的验证并关闭浏览器窗口。
func (s *Service) CancelManualVerify() error {
	s.verifyMu.Lock()
	session := s.verify
	s.verifyMu.Unlock()
	if session == nil || !session.alive() {
		return errors.New("没有进行中的验证会话")
	}
	session.update(VerifyCanceled, "已取消验证，窗口即将关闭")
	session.cancel()
	return nil
}

func (s *Service) runManualVerify(ctx context.Context, session *verifySession, account model.DoubaoAccount) {
	defer func() {
		if recovered := recover(); recovered != nil {
			session.update(VerifyFailed, fmt.Sprintf("验证流程异常退出：%v", recovered))
		}
	}()
	cookieHeader := account.CookieHeader

	bin := ""
	if candidates := browserCandidates(); len(candidates) > 0 {
		bin = candidates[0]
	}
	if bin == "" {
		session.update(VerifyFailed, "本机未找到 Chrome / Edge，无法打开验证窗口。请在浏览器手动登录该账号后，用「粘贴 Cookie」方式更新账号")
		return
	}

	// 按账号持久化 profile：同一账号每次验证复用同一台"设备"（localStorage 里的
	// web_id/device_id 等持续累积可信度），不再每次全新环境；Cookie 预注入会覆盖登录态，
	// 不会串号。profile 仅用于该账号的验证窗口。
	profileDir := filepath.Join(os.TempDir(), fmt.Sprintf("yingce-%s-verify", session.site), "acct-"+account.ID)

	// 指纹对齐：生成通道用账号落库的 UA + 设备 ID，验证窗口也必须一致，
	// 否则风控看到「老登录态 + 陌生新设备」会把验证设计成过不去（5014 循环）。
	launchOpts := launcher.New().
		Bin(bin).
		Headless(false).
		UserDataDir(profileDir).
		// Delete rod 默认的 --enable-automation：该标记会点亮 navigator.webdriver
		// 和「Chrome 正受自动软件控制」横幅，字节风控据此直接把验证判死（5014）。
		Delete("enable-automation").
		Set("--disable-blink-features", "AutomationControlled").
		Set("--window-size", "1280,900")
	if account.UserAgent != "" {
		launchOpts.Set("--user-agent", account.UserAgent)
		log.Printf("[doubao] 验证窗口注入账号 UA account=%s(%s)", account.Label, account.ID)
	}

	ctx2, cancel := context.WithTimeout(ctx, verifyTimeout+30*time.Second)
	defer cancel()

	controlURL, err := launchOpts.Launch()
	if err != nil {
		session.update(VerifyFailed, "启动浏览器失败："+err.Error())
		return
	}
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		session.update(VerifyFailed, "连接浏览器失败："+err.Error())
		return
	}
	session.mu.Lock()
	session.hasBrowser = true
	session.mu.Unlock()
	defer func() { _ = browser.Close() }()
	go func() {
		<-ctx2.Done()
		_ = browser.Close()
	}()

	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		session.update(VerifyFailed, "打开页面失败："+err.Error())
		return
	}
	session.mu.Lock()
	session.page = page
	session.mu.Unlock()

	// 双保险：即使日后 rod 恢复了 enable-automation 默认值，页面侧也读不到 webdriver 标记。
	_, _ = page.EvalOnNewDocument(`Object.defineProperty(navigator, 'webdriver', {get: () => undefined});`)

	if err := page.Navigate(session.loginURL); err != nil {
		session.update(VerifyFailed, "打开站点失败："+err.Error())
		return
	}
	// 种入账号设备指纹（localStorage）：字节系 SDK 首访会生成自己的 web_id/device_id，
	// 验证窗口必须在 SDK 读取前覆盖为账号登录时的 ID，配合 Cookie 形成与真机一致的配对。
	// 页面可能仍在跳转，Eval 失败不致命（下方 Reload 前还会补种一次）。
	if seed := fingerprintSeed(account); len(seed) > 0 {
		_, _ = page.Eval(fingerprintSeedJS, seed)
	}
	// 预注入该账号的全部 Cookie，打开即是该账号的登录态；用户只需过滑块/安全验证。
	domain := siteCookieDomain(session.site)
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
	if len(params) > 0 {
		_ = page.SetCookies(params)
		_ = page.Reload()
		// Reload 后再种一次：首次加载时 SDK 可能已覆盖我们的键值，
		// 注入 Cookie 后的这次覆盖保证刷新后的页面读到的是账号设备 ID。
		if seed := fingerprintSeed(account); len(seed) > 0 {
			_, _ = page.Eval(fingerprintSeedJS, seed)
			_ = page.Reload()
		}
	}
	session.update(VerifyWaiting, fmt.Sprintf(
		"验证窗口已打开%s（已预注入账号「%s」的登录态）。请在窗口中随便发一条消息并完成弹出的滑块/安全验证，完成后回到这里点「完成验证，保存 Cookie」",
		siteDisplayName(session.site), session.accountLabel))

	// 轮询等待：用户点「完成验证」走 Capture，或超时/取消。顺带检测登录态变化
	//（Cookie 失效被登出后重新登录）给出提示，但不自动回收——验证是否通过只有用户知道。
	lastSID := firstSessionID(cookieHeader)
	deadline := time.Now().Add(verifyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return // canceled / capture 完成，状态已更新
		case <-time.After(verifyPollInterval):
		}
		cookies, err := page.Cookies([]string{session.loginURL})
		if err != nil {
			continue
		}
		sid := ""
		for _, cookie := range cookies {
			if cookie.Name == "sessionid" && cookie.Value != "" {
				sid = cookie.Value
				break
			}
		}
		if sid == "" || sid == lastSID {
			continue
		}
		lastSID = sid
		session.mu.Lock()
		if session.state == VerifyWaiting {
			session.message = "检测到新的登录态。如窗口中还有滑块/安全验证请继续完成，完成后点「完成验证，保存 Cookie」"
		}
		session.mu.Unlock()
	}

	session.update(VerifyExpired, "验证超时：窗口已关闭，请重新发起验证")
}

// firstSessionID 从 Cookie 头中提取 sessionid（无则空串），用于对比登录态变化。
func firstSessionID(cookieHeader string) string {
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		if idx := strings.Index(part, "="); idx > 0 && strings.EqualFold(part[:idx], "sessionid") {
			return part[idx+1:]
		}
	}
	return ""
}

// fingerprintSeed 把账号落库的设备 ID 整理成 localStorage 种子；键名覆盖
// captureFingerprint（fingerprint.go）扫描的同族键（web.?id / device.?id / tea.?uuid）。
// 空 ID 不种，避免写入空值干扰 SDK 判断。
func fingerprintSeed(account model.DoubaoAccount) map[string]string {
	seed := map[string]string{
		"web_id":      account.WebID,
		"tt_webid":    account.WebID,
		"tt_webid_v2": account.WebID,
		"device_id":   account.DeviceID,
		"tea_uuid":    account.TeaUUID,
	}
	for key, value := range seed {
		if value == "" {
			delete(seed, key)
		}
	}
	return seed
}

// fingerprintSeedJS 在站点 origin 上把种子写入 localStorage；只写字符串，
// 不碰其他键，SDK 读取到与账号登录时一致的设备 ID。
const fingerprintSeedJS = `(seed) => {
  try {
    for (const key of Object.keys(seed)) {
      try { localStorage.setItem(key, String(seed[key])); } catch (e) {}
    }
    return Object.keys(seed).length;
  } catch (e) { return 0; }
}`
