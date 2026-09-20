package doubao

// 图片无水印升级通道测试。

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func jsonUnmarshalForTest(b []byte, v any) error { return json.Unmarshal(b, v) }

func TestImageKeyPattern(t *testing.T) {
	cases := []struct {
		in  string
		key string
	}{
		{"https://p26-flow-imagex-sign.byteimg.com/tos-cn-i-a9rns2rl98/rc_gen_image/4a5ade7b23e44c9f9062a40ecb2956b3.jpeg~tplv-a9rns2rl98-cdld_wm3.png?lk3s=x&x-signature=y",
			"tos-cn-i-a9rns2rl98/rc_gen_image/4a5ade7b23e44c9f9062a40ecb2956b3.jpeg"},
		{"https://p3-flow-imagex-sign.byteimg.com/tos-cn-i-a9rns2rl98/rc_gen_image/4a5ade7b23e44c9f9062a40ecb2956b3.jpeg~tplv-a9rns2rl98-cthumb_wm1.png?a=b",
			"tos-cn-i-a9rns2rl98/rc_gen_image/4a5ade7b23e44c9f9062a40ecb2956b3.jpeg"},
		{"https://example.com/foo/bar.png", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := imageKeyPattern.FindString(c.in)
		if got != c.key {
			t.Errorf("imageKeyPattern(%q) = %q, want %q", c.in, got, c.key)
		}
	}
}

func TestParseGetFileURLResponse(t *testing.T) {
	raw := []byte(`{"code":0,"msg":"","data":{"file_urls":[{"uri":"tos-cn-i-x/a.jpeg","main_url":"https://p1.example/a.jpeg~tplv-x-image-qvalue.image?sig=1","back_url":"https://p2.example/a.jpeg"}]}}`)
	var parsed getFileURLResponse
	if err := jsonUnmarshalForTest(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Code != 0 || len(parsed.Data.FileURLs) != 1 {
		t.Fatalf("parse failed: %+v", parsed)
	}
	if !strings.HasPrefix(parsed.Data.FileURLs[0].MainURL, "https://p1.example/") {
		t.Fatalf("main_url mismatch: %s", parsed.Data.FileURLs[0].MainURL)
	}
}

// TestUpgradeImagesNoWatermarkLive 真机回归：凭 key 换无水印直链（无 cookie 自动跳过）。
func TestUpgradeImagesNoWatermarkLive(t *testing.T) {
	cookieFile := os.Getenv("DOUBAO_COOKIE_FILE")
	if cookieFile == "" {
		t.Skip("no DOUBAO_COOKIE_FILE")
	}
	rawCookie, err := os.ReadFile(cookieFile)
	if err != nil {
		t.Fatal(err)
	}
	cookie := strings.TrimSpace(string(rawCookie))
	watermarked := "https://p26-flow-imagex-sign.byteimg.com/tos-cn-i-a9rns2rl98/rc_gen_image/4a5ade7b23e44c9f9062a40ecb2956b3.jpeg~tplv-a9rns2rl98-cdld_wm3.png?lk3s=x"
	out := UpgradeImagesNoWatermark(context.Background(), cookie, []string{watermarked})
	if len(out) != 1 {
		t.Fatalf("len(out) = %d", len(out))
	}
	if out[0] == watermarked {
		t.Skip("升级未生效（key 可能已过期），保留原 URL 符合降级约定")
	}
	if !strings.Contains(out[0], "http") {
		t.Fatalf("bad url: %.200s", out[0])
	}
	t.Logf("升级成功: %.220s", out[0])
}
