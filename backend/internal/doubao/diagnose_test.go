package doubao

// 临时诊断：拉会话列表与会话页，打印视频节点全部字段与 SSR 中出现的 URL 主机分布。
// 用法：DOUBAO_COOKIE_FILE=<cookie文件> go test ./internal/doubao/ -run TestDiagnoseDownloadURL -v

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDiagnoseDownloadURL(t *testing.T) {
	cookieFile := os.Getenv("DOUBAO_COOKIE_FILE")
	if cookieFile == "" {
		t.Skip("no DOUBAO_COOKIE_FILE")
	}
	raw, err := os.ReadFile(cookieFile)
	if err != nil {
		t.Fatal(err)
	}
	cookie := strings.TrimSpace(string(raw))
	ctx := context.Background()
	tabID := randomUUID()

	threads := listThreads(ctx, cookie, tabID)
	if len(threads) == 0 {
		t.Fatal("thread/list 为空或失败")
	}
	hostCount := map[string]int{}
	hostRe := regexp.MustCompile(`https://([a-zA-Z0-9.-]+)/`)
	for _, th := range threads {
		html, err := fetchConversationPage(ctx, cookie, th.ID, tabID)
		if err != nil || html == "" {
			t.Logf("会话 %s 拉取失败 err=%v", th.ID, err)
			continue
		}
		for _, m := range hostRe.FindAllStringSubmatch(html, -1) {
			host := m[1]
			if strings.Contains(host, "doubao") || strings.Contains(host, "videopc") ||
				strings.Contains(host, "douyinvod") || strings.Contains(host, "365yg") ||
				strings.Contains(host, "vlabvod") || strings.Contains(host, "byte") {
				hostCount[host]++
			}
		}
		videos := ExtractVideosFromPage(html)
		if len(videos) > 0 {
			t.Logf("=== 会话 %s 视频数 %d", th.ID, len(videos))
			// dump 原始 SSR 里 download_url 附近结构
			for _, v := range videos {
				t.Logf("  URL=%.160s", v.URL)
				if dump := os.Getenv("DOUBAO_URL_DUMP"); dump != "" {
					f, _ := os.OpenFile(dump, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
					f.WriteString(v.URL + "\n")
					f.Close()
				}
			}
			if idx := strings.Index(html, "videopc-download"); idx >= 0 {
				start := idx - 800
				if start < 0 {
					start = 0
				}
				t.Logf("videopc-download 上下文：%s", strings.ReplaceAll(html[start:idx+900], "\n", " "))
			}
		}
	}
	t.Logf("视频相关主机分布：%v", hostCount)
	fmt.Println("done")
}
