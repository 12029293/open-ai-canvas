package doubao

// 临时诊断：探测豆包下载接口，寻找签发 videopc-download 无水印地址的 API。
import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestProbeDownloadAPI(t *testing.T) {
	cookieFile := os.Getenv("DOUBAO_COOKIE_FILE")
	if cookieFile == "" {
		t.Skip("no DOUBAO_COOKIE_FILE")
	}
	raw, _ := os.ReadFile(cookieFile)
	cookie := strings.TrimSpace(string(raw))
	vid := os.Getenv("DOUBAO_VID")
	if vid == "" {
		vid = "v0269cg10004dam811q7dld1ah7ck2m0"
	}
	ctx := context.Background()
	tabID := randomUUID()

	endpoints := []string{
		"/samantha/media/get_video_download_info",
		"/samantha/media/get_download_info",
		"/samantha/media/download_info",
		"/samantha/media/get_video_info",
		"/samantha/media/get_media_info",
		"/samantha/media/get_play_info",
	}
	bodies := []map[string]any{
		{"key": vid, "type": "video"},
		{"video_id": vid, "type": "video"},
		{"media_id": vid, "type": "video"},
	}
	for _, ep := range endpoints {
		for _, body := range bodies {
			reqURL := doubaoOrigin + ep + "?" + buildQuery(ctx, cookie, tabID).Encode()
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			req, err := http.NewRequestWithContext(cctx, http.MethodPost, reqURL, strings.NewReader(mustJSON(body)))
			if err != nil {
				cancel()
				continue
			}
			h := buildBrowserHeaders(ctx, cookie)
			h.Set("Accept", "application/json")
			req.Header = h
			res, err := httpClient.Do(req)
			if err != nil {
				cancel()
				continue
			}
			data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
			res.Body.Close()
			cancel()
			code := struct {
				Code int64 `json:"code"`
			}{}
			_ = json.Unmarshal(data, &code)
			hasDownload := strings.Contains(string(data), "videopc-download") || strings.Contains(string(data), "download_url")
			t.Logf("%s %v -> HTTP %d code=%d len=%d downloadHit=%v", ep, body, res.StatusCode, code.Code, len(data), hasDownload)
			if hasDownload {
				re := regexp.MustCompile(`https://[a-zA-Z0-9.-]+/[^\"]{0,120}`)
				for _, u := range re.FindAllString(string(data), -1) {
					if strings.Contains(u, "videopc") || strings.Contains(u, "download") {
						t.Logf("   HIT: %s", u)
					}
				}
				os.WriteFile("C:/Users/Administrator/Desktop/open-ai-canvas-main/.local/tmp-nomark/hit_"+strings.ReplaceAll(ep, "/", "_")+".json", data, 0o644)
			}
		}
	}
}
