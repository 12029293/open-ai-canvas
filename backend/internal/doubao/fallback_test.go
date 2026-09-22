package doubao

// 检查自己会话页 SSR 是否包含 fallback_api（/video/fplay/）
import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCheckFallbackAPI(t *testing.T) {
	cookieFile := os.Getenv("DOUBAO_COOKIE_FILE")
	if cookieFile == "" {
		t.Skip("no cookie")
	}
	raw, _ := os.ReadFile(cookieFile)
	cookie := strings.TrimSpace(string(raw))
	ctx := context.Background()
	tabID := randomUUID()
	convID := os.Getenv("DOUBAO_CONV")
	if convID == "" {
		convID = "38419344789463298"
	}
	html, err := fetchConversationPage(ctx, cookie, convID, tabID)
	if err != nil || html == "" {
		t.Fatalf("页面拉取失败 err=%v", err)
	}
	t.Logf("页面大小: %d", len(html))
	t.Logf("包含 fallback_api: %v", strings.Contains(html, "fallback_api"))
	t.Logf("包含 video/fplay: %v", strings.Contains(html, "video/fplay"))
	t.Logf("包含 data-fn-args: %v", strings.Contains(html, "data-fn-args"))
	t.Logf("包含 key_seed: %v", strings.Contains(html, "key_seed"))
	t.Logf("包含 qAAB: %v", strings.Contains(html, "qAAB"))
	if idx := strings.Index(html, "fallback_api"); idx >= 0 {
		t.Logf("fallback_api 上下文: %.400s", html[max(0, idx-200):idx+200])
	}
}
