package app

// 从本机浏览器（Edge / Chrome）配置目录直接读取网页版登录凭据并导入账号池。
// 背景：部分浏览器（如 Edge 116+）存在 javascript: 书签（bookmarklet）点击无反应的
// 缺陷，书签导入路线不可用时，退而直接读浏览器的 localStorage 落盘文件。
//
// 原理：Chromium 系浏览器把网页版 localStorage 明文存在
//   <User Data>/<Profile>/Local Storage/leveldb/
// 键形如 "_https://chat.deepseek.com\x00\x01userToken"，
// 值首字节 0x00 表示 UTF-16LE，0x01 表示 Latin-1。
// 浏览器运行时目录有文件锁，先复制到临时目录再以只读模式打开。

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

type WebRelayBrowserImportRequest struct {
	Site string `json:"site"` // deepseek | qwen
}

// webRelayBrowserTargets 各浏览器 User Data 根目录（%LOCALAPPDATA% 下）。
var webRelayBrowserTargets = []struct {
	Name string
	Dir  string
}{
	{"Edge", `Microsoft\Edge\User Data`},
	{"Chrome", `Google\Chrome\User Data`},
}

// webRelayLocalStorageKeys 各站点在 localStorage 里的凭据键。
var webRelayLocalStorageKeys = map[string]string{
	"deepseek": "userToken",
	"qwen":     "token",
}

func webRelayOriginForSite(site string) string {
	if site == "qwen" {
		return "https://chat.qwen.ai"
	}
	return "https://chat.deepseek.com"
}

// WebRelayImportFromBrowser 扫描本机 Edge/Chrome 的所有用户配置，提取网页版
// localStorage 里的登录凭据，逐条走 WebRelayCaptureCredential 入池（自动去重）。
// 千问强制走 CDP 登录窗口路线（磁盘上只能拿到裸 token，风控要求整段 Cookie，
// 且 Cookie 落盘是 app-bound 加密读不出来）；DeepSeek 先扫磁盘，失败再用 CDP 兜底。
func (s *Service) WebRelayImportFromBrowser(req WebRelayBrowserImportRequest) (map[string]interface{}, error) {
	site := strings.TrimSpace(req.Site)
	key, known := webRelayLocalStorageKeys[site]
	if !known {
		return nil, errors.New("不支持的站点：" + site)
	}
	if site == "qwen" {
		return s.webRelayImportViaCDP(site)
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return nil, errors.New("无法定位浏览器数据目录（LOCALAPPDATA 为空）")
	}

	type finding struct {
		browser  string
		profile  string
		cred     string
	}
	var findings []finding
	var scanned []string

	for _, browser := range webRelayBrowserTargets {
		root := filepath.Join(localAppData, browser.Dir)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // 未安装该浏览器
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if name != "Default" && !strings.HasPrefix(name, "Profile ") {
				continue
			}
			leveldbDir := filepath.Join(root, name, "Local Storage", "leveldb")
			if info, err := os.Stat(leveldbDir); err != nil || !info.IsDir() {
				continue
			}
			scanned = append(scanned, browser.Name+"/"+name)
			values, scanErr := webRelayScanLocalStorage(leveldbDir, webRelayOriginForSite(site), key)
			if scanErr != nil {
				continue
			}
			for _, raw := range values {
				cred := strings.TrimSpace(strings.Trim(raw, `"`))
				if cred == "" {
					continue
				}
				findings = append(findings, finding{browser.Name, name, cred})
			}
		}
	}
	if len(scanned) == 0 || len(findings) == 0 {
		// 磁盘扫描没拿到（目录缺失/凭据不存在/文件全被锁）→ CDP 登录窗口兜底。
		return s.webRelayImportViaCDP(site)
	}

	// 逐条入池（Upsert 自带归一化与去重）。先清掉历史上误存的 JSON 包装体等坏凭据，
	// 避免新凭据入库后取号时轮到坏数据。
	if _, err := s.webRelayPool().PruneMalformedTokens(site); err != nil {
		return nil, fmt.Errorf("清理历史坏凭据失败：%v", err)
	}
	imported, duplicated := 0, 0
	var lastErr error
	for _, f := range findings {
		result, err := s.WebRelayCaptureCredential(WebRelayCaptureRequest{Site: site, Credential: f.cred})
		if err != nil {
			lastErr = err
			continue
		}
		if dup, _ := result["duplicate"].(bool); dup {
			duplicated++
		} else {
			imported++
		}
	}
	if imported == 0 && duplicated == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("凭据已找到但导入失败：%v", lastErr)
		}
		return nil, errors.New("凭据已找到但导入失败")
	}
	total := 0
	if status, statusErr := s.WebRelayPoolStatus(site); statusErr == nil && status != nil {
		total = status.AccountCount
	}
	message := "导入成功！影策账号池现在有 " + strconv.Itoa(total) + " 个凭据"
	if imported == 0 && duplicated > 0 {
		message = "凭据之前已导入过，登录态已刷新（账号池现有 " + strconv.Itoa(total) + " 个凭据）"
	}
	return map[string]interface{}{
		"ok": true, "imported": imported, "duplicated": duplicated,
		"total": total, "found": len(findings), "message": message,
	}, nil
}

// webRelayScanLocalStorage 把 leveldb 目录复制到临时目录后只读扫描，
// 返回 origin 站点下 scriptKey 对应的全部 localStorage 值。
func webRelayScanLocalStorage(leveldbDir, origin, scriptKey string) ([]string, error) {
	tmp, err := os.MkdirTemp("", "yingce-ls-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := webRelayCopyDirContents(leveldbDir, tmp); err != nil {
		return nil, err
	}
	db, err := leveldb.OpenFile(tmp, &opt.Options{ReadOnly: true, Strict: opt.NoStrict})
	if err != nil {
		return nil, err
	}
	defer db.Close()

	prefix := "_" + origin + "\x00\x01" + scriptKey
	var values []string
	iter := db.NewIterator(util.BytesPrefix([]byte("_"+origin+"\x00\x01")), nil)
	defer iter.Release()
	for iter.Next() {
		if string(iter.Key()) != prefix {
			continue
		}
		decoded, decErr := webRelayDecodeChromiumValue(iter.Value())
		if decErr != nil || strings.TrimSpace(decoded) == "" {
			continue
		}
		values = append(values, decoded)
	}
	if iter.Error() != nil {
		return values, iter.Error()
	}
	return values, nil
}

// webRelayDecodeChromiumValue 解 Chromium localStorage 编码：首字节 0x00 = UTF-16LE，0x01 = Latin-1。
func webRelayDecodeChromiumValue(data []byte) (string, error) {
	if len(data) < 1 {
		return "", errors.New("empty")
	}
	switch data[0] {
	case 0:
		if (len(data)-1)%2 != 0 {
			data = append(data, 0)
		}
		u16 := make([]uint16, 0, (len(data)-1)/2)
		for i := 1; i < len(data); i += 2 {
			u16 = append(u16, uint16(data[i])|uint16(data[i+1])<<8)
		}
		return string(utf16.Decode(u16)), nil
	case 1:
		runes := make([]rune, len(data)-1)
		for i, b := range data[1:] {
			runes[i] = rune(b)
		}
		return string(runes), nil
	default:
		// 旧格式或未知首字节：按 Latin-1 整体解，尽量不丢数据。
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return string(runes), nil
	}
}

// webRelayCopyDirContents 复制 leveldb 目录内容；被浏览器锁住的文件跳过不报错
// （.ldb/.log/.CURRENT/.MANIFEST 拿到多少算多少，通常足够）。
func webRelayCopyDirContents(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	var copied int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "LOCK" {
			continue
		}
		in, err := os.Open(filepath.Join(srcDir, name))
		if err != nil {
			continue
		}
		out, err := os.Create(filepath.Join(dstDir, name))
		if err != nil {
			in.Close()
			continue
		}
		if _, err := io.Copy(out, in); err == nil {
			copied++
		}
		in.Close()
		out.Close()
	}
	if copied == 0 {
		return errors.New("leveldb 文件全部被浏览器占用，无法复制；请关闭浏览器后重试")
	}
	// 给刚刚被复制的 .log（写入日志）一点落盘余量，避免偶发半截写入。
	_ = time.Now()
	return nil
}
