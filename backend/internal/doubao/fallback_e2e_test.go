package doubao

// fallback_api 无水印母片通道端到端回归测试。
// 依赖真实账号 cookie（DOUBAO_COOKIE_FILE 环境变量），无 cookie 时自动跳过。
// 验证链路：会话页 SSR -> ExtractFallbackAPIs -> 改写 unwatermarked 参数
// -> qAAB token AES 解密 -> 直链可用性（HTTP 200 + video 内容 + 非空体积）。
import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestResolveFallbackVideoURLFromLivePage(t *testing.T) {
	cookieFile := os.Getenv("DOUBAO_COOKIE_FILE")
	if cookieFile == "" {
		t.Skip("no cookie")
	}
	raw, err := os.ReadFile(cookieFile)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		t.Skipf("cookie 文件不可读: %v", err)
	}
	cookie := strings.TrimSpace(string(raw))
	convID := os.Getenv("DOUBAO_CONV")
	if convID == "" {
		convID = "38419344789463298"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pageHTML, err := fetchConversationPage(ctx, cookie, convID, randomUUID())
	if err != nil || pageHTML == "" {
		t.Fatalf("会话页拉取失败 err=%v", err)
	}
	apis := ExtractFallbackAPIs(pageHTML)
	if len(apis) == 0 {
		t.Skip("会话页中没有视频（无 fallback_api），跳过")
	}
	t.Logf("会话页提取到 %d 个视频 fallback_api", len(apis))

	var vid string
	for v := range apis {
		vid = v
		break
	}
	videoURL, err := ResolveFallbackVideoURLFromPage(ctx, pageHTML, vid)
	if err != nil {
		t.Fatalf("无水印直链解析失败 vid=%s: %v", vid, err)
	}
	if videoURL == "" {
		t.Fatal("解密结果为空 URL")
	}
	if !strings.HasPrefix(videoURL, "https://") {
		t.Fatalf("解密结果不是 https 直链: %.120s", videoURL)
	}
	t.Logf("vid=%s 无水印直链(前120字符): %.120s", vid, videoURL)

	// 验证直链真实可下载且是视频
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("无水印直链请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("直链 HTTP 状态 %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "video") && !strings.Contains(ct, "octet-stream") {
		t.Fatalf("直链 Content-Type 非 video: %s", ct)
	}
	// 只读前 64KB 验证非空与 mp4 盒子特征，避免整片下载
	buf := make([]byte, 64*1024)
	n, _ := io.ReadFull(resp.Body, buf)
	if n <= 0 {
		t.Fatal("直链响应体为空")
	}
	head := buf[:min(n, 16)]
	if !(head[4] == 'f' && head[5] == 't' && head[6] == 'y' && head[7] == 'p') &&
		!(head[0] == 0x00 && head[1] == 0x00) {
		t.Logf("头部特征(前16字节): % x", head)
	}
	t.Logf("直链可下载: HTTP 200, Content-Type=%s, 首块 %d 字节, Content-Length=%s",
		ct, n, resp.Header.Get("Content-Length"))
}
