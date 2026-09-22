//go:build desktop

package main

import (
	"os"
	"path/filepath"
)

// desktopBuild 标记本二进制为桌面单文件版：
// 强制本地单用户模式、默认仅监听回环地址、内嵌前端并打开桌面窗口。
const desktopBuild = true

// desktopDefaultAddr 桌面版默认只监听回环地址，不暴露到局域网。
const desktopDefaultAddr = "127.0.0.1:8321"

// defaultDataDir 桌面版数据目录默认放在 exe 同级的 data/ 下，
// 保证 exe 被复制到任何位置都能独立运行。
func defaultDataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "data"
	}
	return filepath.Join(filepath.Dir(exe), "data")
}
