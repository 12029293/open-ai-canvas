package model

import (
	"regexp"
	"strings"
	"testing"
)

func TestProxyURLStatic(t *testing.T) {
	p := &NetworkProxy{Protocol: "http", Host: "sg.nexip.cc", Port: 443, Username: "alice", Password: "p@ss"}
	want := "http://alice:p%40ss@sg.nexip.cc:443"
	if got := p.ProxyURL(); got != want {
		t.Fatalf("ProxyURL() = %q, want %q", got, want)
	}
	if p.NeedsDynamicSID() {
		t.Fatal("静态代理不应判定为动态会话")
	}
}

func TestProxyURLDynamicSID(t *testing.T) {
	p := &NetworkProxy{Protocol: "http", Host: "sg.nexip.cc", Port: 443, Username: "nexuser-sid-{sid}", Password: "secret"}
	if !p.NeedsDynamicSID() {
		t.Fatal("含 {sid} 占位符应判定为动态会话")
	}
	pattern := regexp.MustCompile(`^http://nexuser-sid-\d{8}:secret@sg\.nexip\.cc:443$`)
	first := p.ProxyURL()
	if !pattern.MatchString(first) {
		t.Fatalf("动态代理地址格式不符：%q", first)
	}
	if strings.Contains(first, "{sid}") {
		t.Fatalf("渲染结果不应残留占位符：%q", first)
	}
	// 每次调用生成新 sid（1e8 取值空间内连续两次碰撞概率可忽略）。
	second := p.ProxyURL()
	if first == second {
		t.Fatalf("两次渲染应产生不同会话标识，实际均为 %q", first)
	}
	// 显式渲染使用给定 sid。
	if got := p.RenderProxyURL("12345678"); !strings.Contains(got, "nexuser-sid-12345678:secret@") {
		t.Fatalf("RenderProxyURL 未使用指定 sid：%q", got)
	}
}

func TestProxyURLDynamicNoCredential(t *testing.T) {
	p := &NetworkProxy{Protocol: "socks5", Host: "gw.example.com", Port: 1080, Username: "{sid}"}
	got := p.ProxyURL()
	// 仅有占位符时仍需渲染出会话标识作为用户名。
	if !regexp.MustCompile(`^socks5://\d{8}@gw\.example\.com:1080$`).MatchString(got) {
		t.Fatalf("无密码动态代理地址格式不符：%q", got)
	}
}

func TestProxyURLEmptyHost(t *testing.T) {
	p := &NetworkProxy{Protocol: "http", Host: "", Port: 443, Username: "u-{sid}"}
	if got := p.ProxyURL(); got != "" {
		t.Fatalf("主机为空应返回空地址，实际 %q", got)
	}
}

func TestRenderStableProxyURL(t *testing.T) {
	p := &NetworkProxy{Protocol: "http", Host: "sg.nexip.cc", Port: 443, Username: "nexuser-sid-{sid}", Password: "secret"}
	// 同一种子恒定得到同一 sid（账号级固定出口）。
	first := p.RenderStableProxyURL("account-1")
	second := p.RenderStableProxyURL("account-1")
	if first == "" || first != second {
		t.Fatalf("同一种子应渲染相同地址：%q vs %q", first, second)
	}
	if !regexp.MustCompile(`^http://nexuser-sid-\d{8}:secret@sg\.nexip\.cc:443$`).MatchString(first) {
		t.Fatalf("稳定代理地址格式不符：%q", first)
	}
	// 不同种子应得到不同 sid（1e8 取值空间内碰撞概率可忽略）。
	other := p.RenderStableProxyURL("account-2")
	if other == first {
		t.Fatalf("不同种子不应碰撞，实际均为 %q", first)
	}
	// 静态代理不受 seed 影响。
	static := &NetworkProxy{Protocol: "http", Host: "sg.nexip.cc", Port: 443, Username: "alice"}
	if got := static.RenderStableProxyURL("account-1"); got != "http://alice@sg.nexip.cc:443" {
		t.Fatalf("静态代理应忽略 seed：%q", got)
	}
}
