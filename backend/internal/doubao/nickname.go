// 扫码登录成功后，从已登录页面提取当前账号的豆包昵称，用作账号池显示名。
// 提取是尽力而为：任何一步失败都返回空串，Upsert 回退到默认命名（「豆包账号 N」）。
//
// 取值优先级：
//  1. 同源 fetch 字节系 passport 账号信息接口（data.screen_name / nickname 等）；
//  2. 页面 SSR 全局状态（_ROUTER_DATA / __INITIAL_STATE__）里带 user_id 的用户对象。
//
// 手机号掩码形态（如 138****1234）不算昵称，直接丢弃。

package doubao

import (
	"regexp"
	"strings"

	"github.com/go-rod/rod"
)

// nicknameJS 在已登录页面内执行：先试 passport 接口，再扫 SSR 全局状态。
// 整体限时 6 秒，避免个别接口挂起拖住登录流程。
const nicknameJS = `async () => {
  const overall = new Promise((resolve) => {
    const clean = (s) => (typeof s === 'string' ? s.trim() : '');
    const pickName = (obj) => {
      if (!obj || typeof obj !== 'object') return '';
      for (const k of ['nickname', 'nick_name', 'screen_name', 'display_name']) {
        const v = clean(obj[k]);
        if (v) return v;
      }
      return clean(obj.name);
    };
    const hasMaskedPhone = (s) => /\*{2,}/.test(s);
    const main = async () => {
      const endpoints = [
        '/passport/account/info/v2/?aid=497858&device_platform=web&version_code=20800',
        '/passport/account/info/v2/'
      ];
      for (const ep of endpoints) {
        try {
          const res = await fetch(ep, { credentials: 'include' });
          if (!res.ok) continue;
          const j = await res.json();
          const d = j && (j.data || j.user || j);
          const n = pickName(d);
          if (n && !hasMaskedPhone(n)) return n;
        } catch (e) {}
      }
      try {
        const roots = [window._ROUTER_DATA, window.__INITIAL_STATE__, window.__APP_DATA__];
        for (const root of roots) {
          const found = [];
          const walk = (node, depth) => {
            if (!node || typeof node !== 'object' || depth > 12 || found.length) return;
            if (Array.isArray(node)) {
              for (const it of node) walk(it, depth + 1);
              return;
            }
            if ((node.user_id || node.uid || node.user_id_str) !== undefined) {
              const n = pickName(node);
              if (n && !hasMaskedPhone(n)) { found.push(n); return; }
            }
            for (const k in node) walk(node[k], depth + 1);
          };
          walk(root, 0);
          if (found.length) return found[0];
        }
      } catch (e) {}
      return '';
    };
    main().then(resolve);
    setTimeout(() => resolve(''), 6000);
  });
  return await overall;
}`

var (
	nicknameControlPattern = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	nicknameMaskedPattern  = regexp.MustCompile(`\*{2,}`)
)

// pageNickname 从已登录页面提取当前用户昵称；失败返回空串。
func pageNickname(page *rod.Page) string {
	if page == nil {
		return ""
	}
	res, err := page.Eval(nicknameJS)
	if err != nil || res == nil {
		return ""
	}
	return sanitizeNickname(res.Value.String())
}

// sanitizeNickname 清洗昵称：去控制字符与首尾空白，限长 32 字符；
// 掩码手机号不算昵称。
func sanitizeNickname(raw string) string {
	s := strings.TrimSpace(nicknameControlPattern.ReplaceAllString(raw, ""))
	if s == "" || nicknameMaskedPattern.MatchString(s) {
		return ""
	}
	if runes := []rune(s); len(runes) > 32 {
		s = string(runes[:32])
	}
	return s
}

// defaultLabelPattern 默认自动命名的账号显示名（「豆包账号 1」等）。
// 扫码重登时只有默认名才被昵称覆盖，保留用户手动改过的名字。
var defaultLabelPattern = regexp.MustCompile(`^(豆包|即梦|Dola)账号 \d+$`)

// IsDefaultLabel 判断是否为池子自动生成的默认显示名。
func IsDefaultLabel(label string) bool {
	return defaultLabelPattern.MatchString(strings.TrimSpace(label))
}
