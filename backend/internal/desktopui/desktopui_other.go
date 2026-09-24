//go:build !windows

// Package desktopui 非 Windows 平台的桌面窗口占位实现。
package desktopui

import "errors"

// OpenBrowser 非 Windows 平台暂不提供。
func OpenBrowser(url string) error {
	return errors.New("open browser requires windows")
}

// ShowWindow 非 Windows 平台暂不提供桌面窗口。
func ShowWindow(title, url string) error {
	return errors.New("desktop window requires windows")
}
