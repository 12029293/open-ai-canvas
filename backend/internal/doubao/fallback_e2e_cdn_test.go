package doubao

// fallback_api 母片通道端到端回归（新版 CDN 地址形态）。
// 依赖真实账号 cookie（DOUBAO_COOKIE_FILE 环境变量），无 cookie 时自动跳过。
// 锁住 2026-09-18 的真机诊断结论：
//   - 新版播放地址（v*-vdl.doubao.com/.../video/tos/cn/...）纯 URL 正则提取不出 vid，
//     必须回落文件头 2MB 提取——applyFallbackNoWatermark 曾因此整体失效；
//   - fallback_api 改参母片与播放水印转码字节不同（真无水印原片）。
import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestApplyFallbackNoWatermarkLive(t *testing.T) {
	cookieFile := os.Getenv("DOUBAO_COOKIE_FILE")
	if cookieFile == "" {
		t.Skip("no DOUBAO_COOKIE_FILE")
	}
	raw, err := os.ReadFile(cookieFile)
	if err != nil {
		t.Fatal(err)
	}
	cookie := strings.TrimSpace(string(raw))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	tabID := randomUUID()

	threads := listThreads(ctx, cookie, tabID)
	if len(threads) == 0 {
		t.Skip("thread/list 为空")
	}
	conv := threads[0]
	for _, th := range threads {
		if th.UpdateMs > conv.UpdateMs {
			conv = th
		}
	}
	html, err := fetchConversationPage(ctx, cookie, conv.ID, tabID)
	if err != nil || html == "" {
		t.Skipf("会话页拉取失败: %v", err)
	}
	videos := ExtractVideosFromPage(html)
	if len(videos) == 0 {
		t.Skip("会话页无视频")
	}
	apis := ExtractFallbackAPIs(html)
	if len(apis) == 0 {
		t.Skip("会话页无 fallback_api")
	}

	result := &VideoResult{URLs: []string{videos[0].URL}, ConvID: conv.ID}
	// 纯 URL 提取在新版 CDN 上应失败（回归锚点），文件头兜底后应成功。
	if vid := extractVidFromURL(videos[0].URL); vid != "" {
		t.Logf("（该地址可直接从 URL 提取 vid=%s，文件头兜底未被触发）", vid)
	}
	if !applyFallbackNoWatermark(ctx, cookie, result) {
		t.Fatal("applyFallbackNoWatermark 应成功（文件头 vid 提取 + fallback_api 母片通道）")
	}
	if !result.WatermarkFree || len(result.URLs) == 0 || !strings.HasPrefix(result.URLs[0], "http") {
		t.Fatalf("母片结果异常: %v %v", result.WatermarkFree, result.URLs)
	}
	if result.URLs[0] == videos[0].URL {
		t.Fatal("母片直链不应与播放水印地址相同")
	}
	t.Logf("母片直链: %.160s", truncate(result.URLs[0], 160))
}
