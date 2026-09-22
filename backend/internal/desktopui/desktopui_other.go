//go:build !windows

// Package desktopui 非 Windows 平台的桌面窗口占位实现。
package desktopui

import "errors"

// ShowWindow 非 Windows 平台暂不提供桌面窗口。
func ShowWindow(title, url string) error {
	return errors.New("desktop window requires windows")
}
