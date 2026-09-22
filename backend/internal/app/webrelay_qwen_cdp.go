package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"infinite-canvas/backend/internal/webrelay"

	"github.com/gorilla/websocket"
)

// 千问网页中继（CDP UI 模式）：
// 阿里云 WAF 对 chat.qwen.ai 的 /api/v2/chat/completions 做了反爬（RGV587 惩罚），
// 即使带完整浏览器 Cookie + Chrome TLS 指纹也会被拦（实测），因为 UI 请求携带
// 聚安全 SDK 动态生成的 bx-ua/bx-umidtoken/bx-v 头，无法在 Go 侧伪造。
// 因此千问走"驱动专用 Edge 里的真实 UI 发消息 + CDP 抓响应"的中继模式：
// 1) 在专用窗口新建空白会话；2) 填入 textarea 回车（真实 UI 请求，天然全带反爬头）；
// 3) 通过 CDP Network 事件定位该请求，等 loadingFinished 后 getResponseBody 解析 SSE。
// 附件（图片/视频，复刻反推场景）同样走真实 UI：点「+」→「上传附件」菜单激活
// 页面上传通道（未激活时页面会静默丢弃任何塞入的文件）→ 页面内 DataTransfer
// 注入真实 File 字节并派发 change → 页面自己完成 STS 授权 + OSS 直传
// → Network 事件确认上传完成后，再随消息一起发送。
var (
	qwenCdpRelayMu sync.Mutex
)

// qwenCdpDebugPath 千问 CDP 调试日志/截图落盘目录：跟随数据目录（CANVAS_BACKEND_DATA_DIR），
// 部署目录迁移后不再失效（此前硬编码旧部署路径导致日志静默丢失）。
func qwenCdpDebugPath() string {
	if dir := strings.TrimSpace(os.Getenv("CANVAS_BACKEND_DATA_DIR")); dir != "" {
		return filepath.Join(dir, "qwen-cdp-debug.log")
	}
	return filepath.Join("data", "qwen-cdp-debug.log")
}

func qwenCdpDebug(format string, args ...any) {
	f, err := os.OpenFile(qwenCdpDebugPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05 ")+format+"\n", args...)
}

const qwenCDPLoginHint = "NEED_QWEN_LOGIN:已为你弹出千问登录窗口——请在该 Edge 窗口里登录千问网页版（登录一次即可），然后回到影策重试"

type qwenCdpSession struct {
	conn     *websocket.Conn
	answers  chan map[string]any
	requests chan map[string]any
	finished chan string // loadingFinished 的 requestId
	idSeq    int
}

func qwenCdpDial(baseURL, wsURL string) (*qwenCdpSession, error) {
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("连接调试通道失败：%v", err)
	}
	session := &qwenCdpSession{
		conn:     conn,
		answers:  make(chan map[string]any, 32),
		requests: make(chan map[string]any, 64),
		finished: make(chan string, 64),
	}
	go session.readLoop()
	session.call("Network.enable", nil)
	session.call("DOM.enable", nil)
	return session, nil
}

func (s *qwenCdpSession) readLoop() {
	for {
		var msg map[string]any
		if err := s.conn.ReadJSON(&msg); err != nil {
			close(s.answers)
			close(s.requests)
			close(s.finished)
			return
		}
		if _, hasID := msg["id"]; hasID {
			select {
			case s.answers <- msg:
			default:
			}
			continue
		}
		switch method, _ := msg["method"].(string); method {
		case "Network.requestWillBeSent":
			select {
			case s.requests <- msg:
			default:
			}
		case "Network.loadingFinished":
			params, _ := msg["params"].(map[string]any)
			id, _ := params["requestId"].(string)
			if id != "" {
				select {
				case s.finished <- id:
				default:
				}
			}
		}
	}
}

func (s *qwenCdpSession) call(method string, params map[string]any) {
	s.idSeq++
	m := map[string]any{"id": s.idSeq, "method": method}
	if params != nil {
		m["params"] = params
	}
	_ = s.conn.WriteJSON(m)
}

func (s *qwenCdpSession) waitAnswer(wantID int, secs time.Duration) map[string]any {
	deadline := time.Now().Add(secs)
	for time.Now().Before(deadline) {
		select {
		case m, ok := <-s.answers:
			if !ok {
				return nil
			}
			if id, _ := m["id"].(float64); int(id) == wantID {
				return m
			}
		case <-time.After(deadline.Sub(time.Now())):
			return nil
		}
	}
	return nil
}

// eval 执行页面 JS，返回 result.result.value。
func (s *qwenCdpSession) eval(expr string, awaitPromise bool, secs time.Duration) (any, error) {
	s.call("Runtime.evaluate", map[string]any{
		"expression": expr, "returnByValue": true, "awaitPromise": awaitPromise,
	})
	msg := s.waitAnswer(s.idSeq, secs)
	if msg == nil {
		return nil, errors.New("页面响应超时")
	}
	if exception, _ := msg["exceptionDetails"].(map[string]any); exception != nil {
		text, _ := exception["text"].(string)
		return nil, fmt.Errorf("页面执行出错：%s", text)
	}
	result, _ := msg["result"].(map[string]any)
	res, _ := result["result"].(map[string]any)
	return res["value"], nil
}

func (s *qwenCdpSession) evalString(expr string, secs time.Duration) string {
	value, err := s.eval(expr, false, secs)
	if err != nil {
		return ""
	}
	text, _ := value.(string)
	return text
}

// evalObjectID 执行页面 JS 并返回结果对象的 objectId（returnByValue=false）。
// DOM.setFileInputFiles 需要 objectId/nodeId，而元素节点无法 returnByValue。
func (s *qwenCdpSession) evalObjectID(expr string, secs time.Duration) string {
	s.call("Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": false})
	msg := s.waitAnswer(s.idSeq, secs)
	if msg == nil {
		return ""
	}
	if exception, _ := msg["exceptionDetails"].(map[string]any); exception != nil {
		return ""
	}
	result, _ := msg["result"].(map[string]any)
	res, _ := result["result"].(map[string]any)
	objectID, _ := res["objectId"].(string)
	return objectID
}

// qwenCdpEnsureReady 确保专用 Edge 在跑、标签页已登录且处于空白会话。
func (s *Service) qwenCdpEnsureReady() (*qwenCdpSession, error) {
	baseURL, err := webRelayCdpBaseURL(true)
	if err != nil {
		return nil, err
	}
	wsURL, err := webRelayCdpTarget(baseURL, webRelayOriginForSite("qwen")+"/")
	if err != nil {
		return nil, err
	}
	session, err := qwenCdpDial(baseURL, wsURL)
	if err != nil {
		return nil, err
	}
	// 登录检查。
	token := session.evalString(`localStorage.getItem('token') || ''`, 10*time.Second)
	if strings.TrimSpace(token) == "" {
		_ = session.conn.Close()
		return nil, errors.New(qwenCDPLoginHint)
	}
	// 回到空白会话，保证每次任务上下文干净。
	session.call("Page.navigate", map[string]any{"url": webRelayOriginForSite("qwen") + "/"})
	// 等待 textarea 出现 + React 完成水合（水合前事件监听未挂载，发送会静默失败）。
	for i := 0; i < 60; i++ {
		ready := session.evalString(`document.readyState`, 5*time.Second)
		if ready == "complete" || ready == "interactive" {
			ta := session.evalString(`document.querySelector('textarea') ? 'yes' : 'no'`, 5*time.Second)
			if ta == "yes" {
				break
			}
		}
		time.Sleep(time.Second)
	}
	for i := 0; i < 30; i++ {
		hydrated := session.evalString(`(function(){
			const nodes = [document.getElementById('root'), document.body.firstElementChild, document.body];
			for (const n of nodes) {
				if (!n) continue;
				for (const k of Object.keys(n)) {
					if (k.indexOf('react') > -1) return 'hydrated';
				}
			}
			return 'not-yet';
		})()`, 5*time.Second)
		if hydrated == "hydrated" {
			// 再等应用完成启动：users/status 等初始化请求出现后，发送才不会被丢弃。
			bootDeadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(bootDeadline) {
				select {
				case msg, ok := <-session.requests:
					if !ok {
						break
					}
					params, _ := msg["params"].(map[string]any)
					req, _ := params["request"].(map[string]any)
					url, _ := req["url"].(string)
					if strings.Contains(url, "/api/v2/users/status") || strings.Contains(url, "/api/v2/chats/?page=") {
						time.Sleep(2 * time.Second)
						return session, nil
					}
				case <-time.After(bootDeadline.Sub(time.Now())):
				}
			}
			// 没等到也不阻塞：退回原逻辑。
			return session, nil
		}
		time.Sleep(time.Second)
	}
	_ = session.conn.Close()
	return nil, errors.New("千问页面加载超时，请重试")
}

// qwenChatViaCDP 通过专用浏览器 UI 完成一次对话。
// qwenSurveyDismissExpr 检测并应答千问 Studio 偶发的「偏好评估」弹窗
// （"此反馈将帮助我们评估并提升 Qwen Studio 的体验。你更喜欢哪个回复？请选择一个以继续。"）。
// 该弹窗会接管对话流：不二选一，回复就不再继续，中继侧表现为「千问没有返回内容」。
// 应答策略：优先点「跳过」，没有就任选一个「我更喜欢这个回答」（内容取舍不影响解锁）。
const qwenSurveyDismissExpr = `(function(){
	var markers = ['此反馈将帮助我们评估并提升', '你更喜欢哪个回复', '请选择一个以继续'];
	var bodyText = document.body ? (document.body.innerText || '') : '';
	var hit = false;
	for (var i = 0; i < markers.length; i++) { if (bodyText.indexOf(markers[i]) > -1) { hit = true; break; } }
	if (!hit) return 'none';
	var clickables = Array.prototype.slice.call(document.querySelectorAll('button, [role="button"], a'));
	var skip = clickables.find(function(e){ var t = (e.innerText || '').trim(); return t === '跳过' && e.offsetParent !== null; });
	if (skip) { skip.click(); return 'skipped'; }
	var picks = clickables.filter(function(e){ var t = (e.innerText || '').trim(); return t.indexOf('我更喜欢') > -1 && e.offsetParent !== null; });
	if (picks.length) { picks[0].click(); return 'picked'; }
	var spans = Array.prototype.slice.call(document.querySelectorAll('span, div'));
	var alt = spans.find(function(e){ var t = (e.innerText || '').trim(); return (t === '跳过' || t.indexOf('我更喜欢') === 0) && e.offsetParent !== null && e.children.length <= 2; });
	if (alt) { alt.click(); return 'picked-alt'; }
	return 'survey-no-btn';
})()`

// qwenCdpDismissSurvey 尝试识别并应答偏好评估弹窗。
// 返回状态：none（无弹窗）/ skipped / picked / picked-alt（已应答）/ survey-no-btn（有弹窗但没找到按钮）。
func qwenCdpDismissSurvey(session *qwenCdpSession) string {
	state := session.evalString(qwenSurveyDismissExpr, 10*time.Second)
	switch state {
	case "", "none":
		return "none"
	default:
		qwenCdpDebug("step=survey state=%s", state)
		return state
	}
}

func (s *Service) qwenChatViaCDP(ctx context.Context, request webrelay.ChatRequest, handlers webrelay.StreamHandlers) (webrelay.ChatResult, error) {
	qwenCdpRelayMu.Lock()
	defer qwenCdpRelayMu.Unlock()

	session, err := s.qwenCdpEnsureReady()
	if err != nil {
		return webrelay.ChatResult{}, err
	}
	defer session.conn.Close()

	// 附件（图片/视频，复刻反推场景）：先经真实 UI 上传（页面自己完成 STS+OSS
	// 直传，天然带全部风控头），上传完成的附件挂在输入区随下一条消息发送。
	if len(request.Attachments) > 0 {
		if uploadErr := s.qwenCdpUploadAttachments(session, request.Attachments); uploadErr != nil {
			qwenCdpDebug("step=upload-err %v", uploadErr)
			return webrelay.ChatResult{}, uploadErr
		}
	}

	// 组装提示词：系统设定 + 历史 + 当前任务。
	var builder strings.Builder
	// 千问偶发「偏好评估」弹窗会接管对话（必须二选一才能继续），发送前先清一次。
	qwenCdpDismissSurvey(session)
	if sys := strings.TrimSpace(request.SystemPrompt); sys != "" {
		builder.WriteString("【系统设定】\n" + sys + "\n\n")
	}
	for _, msg := range request.History {
		role := "用户"
		if msg.Role == "assistant" {
			role = "助手"
		}
		builder.WriteString("【" + role + "】\n" + msg.Content + "\n\n")
	}
	builder.WriteString("【任务】\n" + request.Prompt)
	prompt := builder.String()

	// 聚焦输入框并逐块"真实键入"（Input.insertText 走浏览器输入管线）。
	qwenCdpDebug("step=focus url=%s", session.evalString("location.href", 5*time.Second))
	if _, err := session.eval(`document.querySelector('textarea').focus()`, false, 10*time.Second); err != nil {
		qwenCdpDebug("step=focus-err %v", err)
		return webrelay.ChatResult{}, errors.New("千问页面输入框不可用，请重试")
	}
	const chunk = 120
	for start := 0; start < len(prompt); {
		endIdx := start + chunk
		if endIdx > len(prompt) {
			endIdx = len(prompt)
		}
		// 按 rune 边界切分，避免截断多字节字符；推进起点必须跟随 endIdx，
		// 否则下一块会从汉字中间开始，残缺字节被页面解码成 U+FFFD 乱码（◆）。
		for endIdx < len(prompt) && (prompt[endIdx]&0xC0) == 0x80 {
			endIdx++
		}
		session.call("Input.insertText", map[string]any{"text": prompt[start:endIdx]})
		time.Sleep(25 * time.Millisecond)
		start = endIdx
	}
	typed := session.evalString(`(document.querySelector('textarea')||{}).value || ''`, 10*time.Second)
	qwenCdpDebug("step=typed len=%d", len(typed))
	if strings.TrimSpace(typed) == "" {
		return webrelay.ChatResult{}, errors.New("千问页面输入失败，请重试")
	}

	// 清空旧请求事件，避免抓到历史请求（含附件上传请求）。
	drainMsg := func(ch chan map[string]any) {
		for {
			select {
			case <-ch:
			default:
				return
			}
		}
	}
	drainStr := func(ch chan string) {
		for {
			select {
			case <-ch:
			default:
				return
			}
		}
	}
	drainMsg(session.requests)
	drainStr(session.finished)

	// 点击"发送"按钮（对元素派发 click；坐标点击会被输入区的透明下拉容器拦截）。
	click := `(function(){
		const btns = Array.from(document.querySelectorAll('button'));
		const target = btns.find(function(b){ const l = b.getAttribute('aria-label')||''; return l.indexOf('发送') > -1 || l.toLowerCase().indexOf('send') > -1; });
		if (!target) return 'no-btn';
		if (target.disabled) return 'disabled';
		target.click();
		return 'clicked';
	})()`
	clickState := session.evalString(click, 10*time.Second)
	qwenCdpDebug("step=click state=%s", clickState)
	if state := clickState; state != "clicked" {
		// 兜底：回车键派发
		enter := `(function(){
			const ta = document.querySelector('textarea');
			if (!ta) return 'no-textarea';
			ta.focus();
			ta.dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true}));
			return 'enter-ok';
		})()`
		if state2 := session.evalString(enter, 10*time.Second); state2 != "enter-ok" {
			return webrelay.ChatResult{}, errors.New("千问页面发送失败，请重试")
		}
	}

	// 等待 UI 发出 completions 请求（新会话需要先建聊天、跳转，可能要十几秒）。
	var requestID string
	buttonTried := false
	tick := 0
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg, ok := <-session.requests:
			if !ok {
				return webrelay.ChatResult{}, errors.New("调试通道断开，请重试")
			}
			params, _ := msg["params"].(map[string]any)
			req, _ := params["request"].(map[string]any)
			url, _ := req["url"].(string)
			if !strings.Contains(url, "alicdn") && !strings.Contains(url, "aplus") && !strings.Contains(url, "googlesyndication") && !strings.Contains(url, "alibaba") && !strings.Contains(url, "taobao") && !strings.Contains(url, "google") {
				qwenCdpDebug("wait-req url=%s", url)
			}
			if strings.Contains(url, "/api/v2/chat/completions") || strings.Contains(url, "/api/chat/completions") {
				requestID, _ = params["requestId"].(string)
			}
		case <-time.After(500 * time.Millisecond):
			tick++
			// 弹窗可能在点发送前后弹出并拦截请求，定期清理；解锁后重发一次。
			if tick%4 == 0 {
				if state := qwenCdpDismissSurvey(session); state != "none" {
					deadline = time.Now().Add(45 * time.Second)
					buttonTried = false
					_ = session.evalString(click, 10*time.Second)
				}
			}
		}
		if requestID == "" && !buttonTried && time.Now().After(deadline.Add(-20*time.Second)) {
			// Enter 没触发时兜底点一次发送按钮。
			buttonTried = true
			_ = session.evalString(`(function(){
				const btns = Array.from(document.querySelectorAll('button'));
				const send = btns.find(function(b){ const l = b.getAttribute('aria-label')||''; return l.indexOf('发送') > -1 || l.toLowerCase().indexOf('send') > -1; }) || document.querySelector('button[type="submit"]');
				if (send) { send.click(); return 'clicked'; }
				return 'no-btn';
			})()`, 10*time.Second)
		}
		if requestID != "" {
			break
		}
	}
	if requestID == "" {
		qwenCdpDebug("step=no-request url=%s valueLen=%d", session.evalString("location.href", 5*time.Second), len(session.evalString(`(document.querySelector('textarea')||{}).value || ''`, 5*time.Second)))
		session.call("Page.captureScreenshot", map[string]any{"format": "png"})
		if shot := session.waitAnswer(session.idSeq, 15*time.Second); shot != nil {
			if result, ok := shot["result"].(map[string]any); ok {
				if b64, _ := result["data"].(string); b64 != "" {
					if data, err := base64.StdEncoding.DecodeString(b64); err == nil {
						_ = os.WriteFile(strings.TrimSuffix(qwenCdpDebugPath(), ".log")+"-fail.png", data, 0o644)
					}
				}
			}
		}
		return webrelay.ChatResult{}, errors.New("千问页面没有发出对话请求——请确认窗口里已登录并可以正常聊天，然后重试")
	}

	// 轮询读取响应体（不依赖 loadingFinished 事件——页面资源完成事件会塞满通道）。
	// SSE 流式响应在完成前 getResponseBody 会报错，完成后返回完整文本。
	finishDeadline := time.Now().Add(15 * time.Minute)
	pollTick := 0
	var body string
	for time.Now().Before(finishDeadline) {
		select {
		case <-ctx.Done():
			return webrelay.ChatResult{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		// 弹窗若在生成中途弹出会冻结回复流，应答后给足继续生成的时间。
		pollTick++
		if pollTick%5 == 0 {
			if state := qwenCdpDismissSurvey(session); state != "none" {
				finishDeadline = time.Now().Add(5 * time.Minute)
			}
		}
		session.call("Network.getResponseBody", map[string]any{"requestId": requestID})
		msg := session.waitAnswer(session.idSeq, 10*time.Second)
		if msg == nil {
			continue
		}
		if result, ok := msg["result"].(map[string]any); ok {
			if text, _ := result["body"].(string); strings.TrimSpace(text) != "" {
				body = text
				break
			}
		}
	}
	if strings.TrimSpace(body) == "" {
		return webrelay.ChatResult{}, errors.New("千问响应等待超时，请重试")
	}
	if strings.Contains(body, "RGV587") || strings.Contains(body, "FAIL_SYS_USER_VALIDATE") {
		return webrelay.ChatResult{}, webrelay.NewRelayError(webrelay.FailKindRateLimited,
			"千问风控拦截（RGV587）：请在弹出的千问窗口里完成一次滑块验证后重试")
	}
	return webrelay.ConsumeQwenStream(strings.NewReader(body), handlers)
}

// ---- 附件上传（复刻反推的视频/图片参考） ----

// qwenCdpUploadAttachments 逐个把任务附件经真实 UI 上传给千问。
// 上传完成后再由调用方键入提示词并点发送，附件随消息一起提交。
func (s *Service) qwenCdpUploadAttachments(session *qwenCdpSession, attachments []webrelay.ChatAttachment) error {
	for index, attachment := range attachments {
		if err := qwenCdpUploadOneAttachment(session, index, attachment); err != nil {
			return err
		}
	}
	time.Sleep(500 * time.Millisecond) // 等输入区附件卡片稳定
	return nil
}

// qwenCdpOpenAttachMenu 走真实 UI 打开附件上传通道：点「+」（选择模式）→ 菜单里点
// 「上传附件」。千问页面只有在菜单点击后才给 input[type=file] 挂上 accept 与 change
// 监听——实测未激活时无论 CDP setFileInputFiles 还是页面内 DataTransfer 注入都会被
// 静默丢弃（input.files 恒为空、零网络请求）。
func qwenCdpOpenAttachMenu(session *qwenCdpSession) error {
	if state := session.evalString(`(function(){
		const btn = document.querySelector('.mode-select-open') || Array.from(document.querySelectorAll('button')).find(function(b){ return (b.getAttribute('aria-label')||'').indexOf('选择模式') > -1; });
		if (!btn) return 'no-btn';
		btn.click();
		return 'clicked';
	})()`, 10*time.Second); state != "clicked" {
		return errors.New("千问页面没有找到「选择模式」入口，无法上传附件")
	}
	menuClicked := ""
	for i := 0; i < 10; i++ {
		menuClicked = session.evalString(`(function(){
			const item = Array.from(document.querySelectorAll('.qwen-chat-v2-dropdown-menu-item')).find(function(i){ return (i.textContent||'').indexOf('上传附件') > -1; });
			if (!item) return 'no-item';
			item.click();
			return 'clicked';
		})()`, 10*time.Second)
		if menuClicked == "clicked" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if menuClicked != "clicked" {
		return errors.New("千问页面没有找到「上传附件」菜单项，请在千问窗口手动确认可以添加附件后重试")
	}
	ready := ""
	for i := 0; i < 10; i++ {
		ready = session.evalString(`(function(){
			const el = document.querySelectorAll('input[type=file]')[0];
			if (!el) return 'no-input';
			return (el.getAttribute('accept')||'') !== '' ? 'ready' : 'not-ready';
		})()`, 10*time.Second)
		if ready == "ready" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if ready != "ready" {
		return errors.New("千问附件上传通道未就绪，请重试")
	}
	return nil
}

// qwenCdpUploadOneAttachment 单个附件：激活上传通道 → 附件字节分块注入页面 →
// DataTransfer 构造真实 File 塞进 input[type=file]（DOM.setFileInputFiles 在该
// 输入框上实测无效，files 始终为空）→ 派发 change，页面自己完成 STS 授权 + OSS 直传。
func qwenCdpUploadOneAttachment(session *qwenCdpSession, index int, attachment webrelay.ChatAttachment) error {
	if len(attachment.Data) == 0 {
		return fmt.Errorf("第 %d 个附件内容为空", index+1)
	}
	if err := qwenCdpOpenAttachMenu(session); err != nil {
		return err
	}
	name := strings.TrimSpace(attachment.Name)
	if name == "" {
		name = "reference" + mediaExtension(attachment.MimeType)
	}
	mimeType := strings.TrimSpace(attachment.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	// 分块传 base64（单条 CDP 消息不宜过大）；按 2MB base64 字符切块，块内字符数
	// 是 4 的倍数，解码边界不会截断。
	const encodedChunk = 2 * 1024 * 1024
	encoded := base64.StdEncoding.EncodeToString(attachment.Data)
	if _, err := session.eval(`window.__yingceUpload = { chunks: [] }; 'ok'`, false, 10*time.Second); err != nil {
		return fmt.Errorf("千问页面缓冲区初始化失败：%v", err)
	}
	for start := 0; start < len(encoded); start += encodedChunk {
		end := start + encodedChunk
		if end > len(encoded) {
			end = len(encoded)
		}
		quoted, _ := json.Marshal(encoded[start:end])
		if _, err := session.eval(`window.__yingceUpload.chunks.push(atob(`+string(quoted)+`)); 'ok'`, false, 30*time.Second); err != nil {
			return fmt.Errorf("千问附件数据注入失败：%v", err)
		}
	}
	nameQuoted, _ := json.Marshal(name)
	mimeQuoted, _ := json.Marshal(mimeType)
	inject := `(function(){
		const bin = (window.__yingceUpload && window.__yingceUpload.chunks || []).join('');
		delete window.__yingceUpload;
		const input = document.querySelectorAll('input[type=file]')[0];
		if (!input) return 'no-input';
		const bytes = new Uint8Array(bin.length);
		for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
		const dt = new DataTransfer();
		dt.items.add(new File([bytes], ` + string(nameQuoted) + `, { type: ` + string(mimeQuoted) + ` }));
		input.files = dt.files;
		if (!input.files.length) return 'rejected';
		input.dispatchEvent(new Event('input', { bubbles: true }));
		input.dispatchEvent(new Event('change', { bubbles: true }));
		return 'injected';
	})()`
	state, err := session.eval(inject, true, 60*time.Second)
	qwenCdpDebug("step=inject index=%d state=%v err=%v", index, state, err)
	if err != nil {
		return fmt.Errorf("千问附件注入页面失败：%v", err)
	}
	if state != "injected" {
		return errors.New("千问页面拒绝了附件注入，请重试")
	}
	qwenCdpDebug("step=injected index=%d name=%s size=%d", index, name, len(attachment.Data))
	return qwenCdpWaitUploadSettled(session, name, 3*time.Minute)
}

// qwenCdpWaitUploadSettled 等待附件真正上传完成：
// 1) 页面发出上传相关请求（STS 授权 / OSS 直传 / 后端文件接口）且对应 body 传输完成；
// 2) 若 20 秒内毫无网络动静，兜底检查临时文件名是否出现在页面（内联上传通道）；
// 3) 两者皆无 → 附件没有开始上传（超限/格式不支持/选错入口），明确报错。
func qwenCdpWaitUploadSettled(session *qwenCdpSession, tempName string, timeout time.Duration) error {
	urls := map[string]string{}
	uploadSeen := false
	silentDeadline := time.Now().Add(20 * time.Second)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case msg, ok := <-session.requests:
			if !ok {
				return errors.New("调试通道断开，请重试")
			}
			params, _ := msg["params"].(map[string]any)
			request, _ := params["request"].(map[string]any)
			id, _ := params["requestId"].(string)
			url, _ := request["url"].(string)
			if id != "" {
				urls[id] = url
			}
			if matchQwenUploadURL(url) {
				uploadSeen = true
			}
			continue
		case id, ok := <-session.finished:
			if !ok {
				return errors.New("调试通道断开，请重试")
			}
			url := urls[id]
			// STS 授权响应极小，完成不代表上传完成；以 OSS 直传/文件接口的
			// body 传输完成为准。
			if uploadSeen && matchQwenUploadBodyDone(url) {
				qwenCdpDebug("step=upload-done url=%s", url)
				return nil
			}
			continue
		case <-time.After(500 * time.Millisecond):
		}
		if !uploadSeen && time.Now().After(silentDeadline) {
			// 没有任何上传网络动静：可能走了内联/其他通道，用文件名做存在性兜底。
			quoted, _ := json.Marshal(tempName)
			visible := session.evalString(
				`document.body && document.body.innerText.indexOf(`+string(quoted)+`) > -1 ? 'yes' : 'no'`,
				10*time.Second)
			qwenCdpDebug("step=silent-fallback visible=%s", visible)
			if visible == "yes" {
				return nil
			}
			return errors.New("千问附件未能开始上传（可能超过网页版大小限制或格式不支持），请缩短视频或在千问窗口手动验证")
		}
	}
	if uploadSeen {
		return errors.New("等待千问附件上传完成超时，请重试或缩短视频")
	}
	return errors.New("千问附件未能开始上传，请重试")
}

func matchQwenUploadURL(url string) bool {
	return strings.Contains(url, "getstsToken") ||
		strings.Contains(url, "/api/v2/files/") ||
		strings.Contains(url, "/api/files/") ||
		strings.Contains(url, "aliyuncs.com")
}

// matchQwenUploadBodyDone 报告某请求的 body 传输完成是否意味着上传已完成。
// STS 授权响应太小不算；OSS 直传（aliyuncs.com）或后端文件接口完成才算。
func matchQwenUploadBodyDone(url string) bool {
	return strings.Contains(url, "aliyuncs.com") ||
		strings.Contains(url, "/api/v2/files/") ||
		strings.Contains(url, "/api/files/")
}
