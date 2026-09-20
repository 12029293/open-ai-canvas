package app

// 千问「登录窗口导入」（CDP 路线）：
// Edge 136+ 禁止在默认用户配置上开 CDP 调试端口，且 Cookie 落盘是 app-bound
// 加密（v20），第三方进程解不开。唯一稳妥路线：让浏览器自己交出解密后的数据。
//
// 做法：启动一个影策专用的 Edge 配置目录（不影响用户主 Edge，可同时运行），
// 打开千问网页版标签页，通过 CDP 读取 localStorage token 与全套 Cookie。
// 专用配置持久化在 %LOCALAPPDATA%\Yingce\edge-profile，用户在该窗口登录一次后，
// 之后每次导入都全自动（若窗口被关，重新拉起即可，登录态仍在配置里）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	webRelayCdpPortBase   = 9223
	webRelayCdpPortSpread = 10
)

var webRelayCdpMu sync.Mutex

// webRelayEdgePath 定位 Edge 可执行文件。
func webRelayEdgePath() string {
	var candidates []string
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		candidates = append(candidates, filepath.Join(local, `Microsoft\Edge\Application\msedge.exe`))
	}
	candidates = append(candidates,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	)
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// webRelayCdpBaseURL 找一个可用的 CDP 端点；autoStart=true 时没找到就拉起专用 Edge。
func webRelayCdpBaseURL(autoStart bool) (string, error) {
	// 已有可用端点（本服务之前拉起的专用实例）直接复用。
	for offset := 0; offset < webRelayCdpPortSpread; offset++ {
		base := fmt.Sprintf("http://127.0.0.1:%d", webRelayCdpPortBase+offset)
		client := &http.Client{Timeout: 400 * time.Millisecond}
		if resp, err := client.Get(base + "/json/version"); err == nil {
			resp.Body.Close()
			return base, nil
		}
	}
	if !autoStart {
		return "", errors.New("CDP 未运行")
	}
	edge := webRelayEdgePath()
	if edge == "" {
		return "", errors.New("没有找到 Microsoft Edge，请确认本机安装了 Edge 浏览器")
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", errors.New("无法定位数据目录（LOCALAPPDATA 为空）")
	}
	profile := filepath.Join(localAppData, "Yingce", "edge-profile")
	// 首选带 breakaway 标志（若被宿主 job 限制则退回普通启动）。
	cmd := exec.Command(edge,
		"--remote-debugging-port=9223",
		"--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动 Edge 失败：%v", err)
	}
	// 等待 CDP 就绪（最多 25 秒）。
	client := &http.Client{Timeout: 600 * time.Millisecond}
	for i := 0; i < 50; i++ {
		time.Sleep(500 * time.Millisecond)
		for offset := 0; offset < webRelayCdpPortSpread; offset++ {
			base := fmt.Sprintf("http://127.0.0.1:%d", webRelayCdpPortBase+offset)
			if resp, err := client.Get(base + "/json/version"); err == nil {
				resp.Body.Close()
				return base, nil
			}
		}
	}
	return "", errors.New("Edge 启动后 CDP 调试端口未就绪")
}

// webRelayCdpTarget 确保站点标签页存在并返回其 webSocketDebuggerUrl。
func webRelayCdpTarget(baseURL, siteURL string) (string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	listTarget := func() map[string]any {
		resp, err := client.Get(baseURL + "/json/list")
		if err != nil {
			return nil
		}
		var targets []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
			resp.Body.Close()
			return nil
		}
		resp.Body.Close()
		for _, t := range targets {
			if url, _ := t["url"].(string); strings.HasPrefix(url, siteURL) {
				return t
			}
		}
		return nil
	}
	target := listTarget()
	if target == nil {
		req, _ := http.NewRequest(http.MethodPut, baseURL+"/json/new?"+siteURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("打开站点标签页失败：%v", err)
		}
		var tab map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&tab)
		resp.Body.Close()
		// 等页面完成首屏（WAF/登录态 Cookie 生效）。
		time.Sleep(5 * time.Second)
		if target = listTarget(); target == nil {
			if ws, _ := tab["webSocketDebuggerUrl"].(string); ws != "" {
				target = tab
			}
		}
	}
	if target == nil {
		return "", errors.New("无法定位站点标签页")
	}
	wsURL, _ := target["webSocketDebuggerUrl"].(string)
	if wsURL == "" {
		return "", errors.New("标签页缺少调试地址")
	}
	return wsURL, nil
}

// webRelayCDPCredentials 通过 CDP 读取站点 localStorage token 与全套 Cookie。
// 未登录时 token 为空、错误为 nil（调用方引导登录）。
func webRelayCDPCredentials(site string) (token string, cookie string, err error) {
	origin := webRelayOriginForSite(site)
	lsKey := webRelayLocalStorageKeys[site]
	baseURL, err := webRelayCdpBaseURL(true)
	if err != nil {
		return "", "", err
	}
	wsURL, err := webRelayCdpTarget(baseURL, origin)
	if err != nil {
		return "", "", err
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("连接调试通道失败：%v", err)
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]any{"id": 1, "method": "Network.enable"})
	_ = conn.WriteJSON(map[string]any{"id": 2, "method": "Runtime.evaluate",
		"params": map[string]any{"expression": "localStorage.getItem('" + lsKey + "') || ''"}})
	_ = conn.WriteJSON(map[string]any{"id": 3, "method": "Network.getCookies",
		"params": map[string]any{"urls": []string{origin, strings.TrimSuffix(origin, "/")}}})

	timeout := time.AfterFunc(15*time.Second, func() { _ = conn.Close() })
	defer timeout.Stop()
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			if token != "" || cookie != "" {
				break
			}
			return "", "", fmt.Errorf("调试通道读取失败：%v", err)
		}
		switch id, _ := msg["id"].(float64); id {
		case 2:
			result, _ := msg["result"].(map[string]any)
			res, _ := result["result"].(map[string]any)
			token, _ = res["value"].(string)
			token = strings.TrimSpace(token)
		case 3:
			result, _ := msg["result"].(map[string]any)
			cookies, _ := result["cookies"].([]any)
			var parts []string
			for _, c := range cookies {
				cm, _ := c.(map[string]any)
				name, _ := cm["name"].(string)
				value, _ := cm["value"].(string)
				if name == "" || value == "" {
					continue
				}
				parts = append(parts, name+"="+value)
			}
			cookie = strings.Join(parts, "; ")
		}
		if token != "" || cookie != "" {
			if msgID, ok := msg["id"].(float64); ok && msgID == 3 {
				break
			}
		}
	}
	return token, cookie, nil
}

// webRelayImportViaCDP 登录窗口导入：CDP 拿全量 Cookie + token，入池。
func (s *Service) webRelayImportViaCDP(site string) (map[string]interface{}, error) {
	webRelayCdpMu.Lock()
	defer webRelayCdpMu.Unlock()
	token, cookie, err := webRelayCDPCredentials(site)
	if err != nil {
		return nil, fmt.Errorf("自动导入遇到问题：%v（可改用下方的手动方式）", err)
	}
	siteName := map[string]string{"qwen": "千问", "deepseek": "DeepSeek"}[site]
	if token == "" {
		return nil, errors.New("NEED_LOGIN:已为你弹出" + siteName + "登录窗口——请在该 Edge 窗口里登录" + siteName + "网页版（登录一次即可，窗口可以留着或最小化），然后回到影策再点一次「从浏览器一键导入」")
	}
	credential := cookie
	if credential == "" {
		credential = token
	}
	if _, err := s.webRelayPool().PruneMalformedTokens(site); err != nil {
		return nil, fmt.Errorf("清理历史坏凭据失败：%v", err)
	}
	result, err := s.WebRelayCaptureCredential(WebRelayCaptureRequest{Site: site, Credential: credential})
	if err != nil {
		return nil, err
	}
	if m, ok := result["message"].(string); ok && site == "qwen" {
		result["message"] = "千问整段 Cookie 已导入：" + m
	}
	return result, nil
}
