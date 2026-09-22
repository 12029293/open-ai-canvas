//go:build !desktop

package main

// 服务端普通构建：桌面特性全部关闭，保持既有行为不变。
const desktopBuild = false

const desktopDefaultAddr = ":8080"

func defaultDataDir() string {
	return "data"
}
