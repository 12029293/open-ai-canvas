//go:build windows

// Package desktopui 负责桌面版打开应用窗口：
// 优先 WebView2 原生窗口，环境不支持时回退系统默认浏览器。
package desktopui

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/jchv/go-webview2"
)

// OpenBrowser 用系统默认浏览器打开 URL（浏览器模式启动路径，不阻塞）。
func OpenBrowser(url string) error {
	return openBrowser(url)
}

// ShowWindow 打开桌面窗口并阻塞到窗口关闭。
// WebView2 不可用时回退浏览器（浏览器方式立即返回，不阻塞）。
func ShowWindow(title, url string) error {
	runtime.LockOSThread()
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  1440,
			Height: 900,
			IconId: 2,
			Center: true,
		},
	})
	if w == nil {
		return openBrowser(url)
	}
	defer w.Destroy()
	w.SetTitle(title)
	w.Navigate(url)
	w.Run()
	return nil
}

func openBrowser(url string) error {
	// rundll32 方式不依赖 PATH 里的 start，也不会挂起当前进程。
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开浏览器失败: %w", err)
	}
	return nil
}
