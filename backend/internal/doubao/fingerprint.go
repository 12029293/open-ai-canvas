package doubao

// 账号登录浏览器指纹：扫码登录 / 手动过验证时从登录页捕获真实的
// User-Agent 与设备 ID（web_id / device_id / tea_uuid），落库到账号上。
// 生成请求经 ctx 注入同账号指纹，与登录时的浏览器环境保持一致——
// 上游对「登录用真浏览器、生成用脚本指纹」的新账号会直接顶点限流（710022002）。

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/ysmood/gson"
)

// AccountFingerprint 生成请求使用的账号级指纹；空字段回退派生值。
type AccountFingerprint struct {
	UserAgent string
	DeviceID  string
	WebID     string
	TeaUUID   string
}

// empty 报告指纹是否完全为空（空 = 不注入 ctx，走派生指纹）。
func (f *AccountFingerprint) empty() bool {
	return f == nil || (f.UserAgent == "" && f.DeviceID == "" && f.WebID == "" && f.TeaUUID == "")
}

type fingerprintCtxKey struct{}

// WithFingerprint 把账号指纹注入 ctx（生成编排取号后调用）。
func WithFingerprint(ctx context.Context, fp *AccountFingerprint) context.Context {
	if fp == nil || fp.empty() {
		return ctx
	}
	return context.WithValue(ctx, fingerprintCtxKey{}, fp)
}

func fingerprintFromCtx(ctx context.Context) *AccountFingerprint {
	if ctx == nil {
		return nil
	}
	fp, _ := ctx.Value(fingerprintCtxKey{}).(*AccountFingerprint)
	return fp
}

// fingerprintJS 在已登录页面内执行：优先扫 performance 资源条目里的
// web_id / device_id / tea_uuid / msToken 参数（页面加载时大量字节系
// API 调用都会带），再用 localStorage 兜底；UA 直接取 navigator。
const fingerprintJS = `() => {
  const clean = (s) => (typeof s === 'string' ? s.trim() : '');
  const found = { ua: '', web_id: '', device_id: '', tea_uuid: '' };
  try { found.ua = clean(navigator.userAgent); } catch (e) {}
  try {
    const scan = (raw) => {
      const text = String(raw || '');
      for (const m of text.matchAll(/[?&](web_id|device_id|tea_uuid)=([0-9]{6,32})/g)) {
        const key = m[1];
        if (!found[key]) found[key] = clean(decodeURIComponent(m[2]));
      }
    };
    for (const e of performance.getEntriesByType('resource')) scan(e.name);
    for (const e of performance.getEntriesByType('navigation')) scan(e.name);
  } catch (e) {}
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i) || '';
      if (!/web.?id|device.?id|tea.?uuid/i.test(key)) continue;
      const value = clean(localStorage.getItem(key));
      if (/^[0-9]{6,32}$/.test(value) && !found[key.includes('device') ? 'device_id' : 'web_id']) {
        found[key.includes('device') ? 'device_id' : 'web_id'] = value;
      }
    }
  } catch (e) {}
  return found;
}`

var fingerprintIDPattern = regexp.MustCompile(`^[0-9]{6,32}$`)

// sanitizeFingerprint 清洗捕获结果：只保留纯数字设备 ID 与合法 UA。
// UA 规则从严：rod 无头 Chromium 的 UA 是 Chrome/114 + Macintosh（2023 年
// headless 默认值，03:03 实测落库），比写死的正常 Chrome UA 更像机器人；
// 只接受主版本 ≥120 的桌面 Chrome UA，其余一律置空走默认（老号验证可用）。
func sanitizeFingerprint(raw map[string]any) *AccountFingerprint {
	str := func(key string) string {
		if v, ok := raw[key].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	fp := AccountFingerprint{
		UserAgent: sanitizeUserAgent(str("ua")),
		WebID:     str("web_id"),
		DeviceID:  str("device_id"),
		TeaUUID:   str("tea_uuid"),
	}
	for _, id := range []*string{&fp.WebID, &fp.DeviceID, &fp.TeaUUID} {
		if !fingerprintIDPattern.MatchString(*id) {
			*id = ""
		}
	}
	if fp.empty() {
		return nil
	}
	return &fp
}

// minTrustedChromeVersion 桌面 Chrome UA 的最低可信主版本。
const minTrustedChromeVersion = 120

// sanitizeUserAgent 校验捕获的 UA：必须是较新的桌面 Chrome 且非无头标记，
// 否则返回空串（生成端回退默认 Chrome/131 Windows UA）。
func sanitizeUserAgent(ua string) string {
	if ua == "" || strings.Contains(ua, "\n") || len(ua) > 300 {
		return ""
	}
	if strings.Contains(ua, "Headless") || strings.Contains(ua, "Electron") {
		return ""
	}
	m := chromeMajorVersionPattern.FindStringSubmatch(ua)
	if m == nil {
		return ""
	}
	for _, digit := range m[1] {
		if digit < '0' || digit > '9' {
			return ""
		}
	}
	// 主版本为纯数字且 ≥ minTrustedChromeVersion（同长度下字典序即数值序）。
	if len(m[1]) != 3 || m[1] < fmt.Sprint(minTrustedChromeVersion) {
		return ""
	}
	return ua
}

// fpCaptureTries 指纹捕获重试次数与间隔：登录成功瞬间页面常在跳转中，
// 单次 Eval 容易落在空执行上下文上（02:26 三次扫码均因此落空），必须重试。
const (
	fpCaptureTries   = 8
	fpCaptureBackoff = 1200 * time.Millisecond
)

// pageFingerprint 从已登录页面捕获浏览器指纹。带重试以跳过页面跳转窗口；
// uaFallback 来自浏览器版本信息，页面 Eval 全灭时仍能保证 UA 落库。
// 返回的 diag 为失败原因摘要（成功时为空串），供调用方记日志定位。
func pageFingerprint(page *rod.Page, uaFallback string) (*AccountFingerprint, string) {
	if page == nil {
		return nil, "page is nil"
	}
	var lastDiag string
	for i := 0; i < fpCaptureTries; i++ {
		if i > 0 {
			time.Sleep(fpCaptureBackoff)
		}
		// 页面可能在导航中：尽力等待加载完成再取值，等待失败不阻断重试。
		_ = page.WaitLoad()
		res, err := page.Eval(fingerprintJS)
		if err != nil {
			lastDiag = "eval: " + err.Error()
			continue
		}
		if res == nil {
			lastDiag = "eval: nil result"
			continue
		}
		fp, diag := fingerprintFromEvalValue(res.Value)
		if fp == nil {
			lastDiag = diag
			continue
		}
		// 页面 JS 拿不到 UA 时用浏览器版本信息兜底（同一浏览器，UA 一致）。
		// 兜底值同样过校验：rod 自带 Chromium 的 UA 过不了，宁可空走默认。
		if fp.UserAgent == "" {
			fp.UserAgent = sanitizeUserAgent(uaFallback)
		}
		if fp.empty() {
			lastDiag = "eval: empty after fallback"
			continue
		}
		return fp, ""
	}
	return nil, lastDiag
}

// fingerprintFromEvalValue 解析 Eval 返回值：gson 对 JS 对象可能给 map，
// 也可能给原始 JSON 字节（[]uint8，02:50 实测如此），两种都按 map 处理。
func fingerprintFromEvalValue(value gson.JSON) (*AccountFingerprint, string) {
	var raw map[string]any
	switch v := value.Raw().(type) {
	case map[string]any:
		raw = v
	case []byte: // gson 懒解析：JS 对象可能仍是原始 JSON 字节（[]uint8 与 []byte 同型）
		if err := json.Unmarshal(v, &raw); err != nil {
			return nil, "eval: json bytes unmarshal: " + err.Error()
		}
	case string:
		if err := json.Unmarshal([]byte(v), &raw); err != nil {
			return nil, "eval: string unmarshal: " + err.Error()
		}
	default:
		return nil, fmt.Sprintf("eval: unexpected result type %T", value.Raw())
	}
	fp := sanitizeFingerprint(raw)
	if fp == nil {
		return nil, "eval: sanitized empty"
	}
	return fp, ""
}
