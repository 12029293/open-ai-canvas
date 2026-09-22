package doubao

import "testing"

func TestCleanVideoURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "替换 lr 参数",
			in:   "https://v.douyinvod.com/a.mp4?lr=cici_ai&watermark=1&sign=x",
			want: "https://v.douyinvod.com/a.mp4?lr=video_gen_no_watermark&watermark=0&sign=x",
		},
		{
			name: "删除水印模板段",
			in:   "https://v.douyinvod.com/a.mp4?lr=video_gen_no_watermark~tplv-xj-mark-watermark.template&sign=x",
			want: "https://v.douyinvod.com/a.mp4?lr=video_gen_no_watermark&sign=x",
		},
		{
			name: "删除 logo 参数",
			in:   "https://v.douyinvod.com/a.mp4?a=1&logo=xxx&sign=x",
			want: "https://v.douyinvod.com/a.mp4?a=1&sign=x",
		},
		{
			name: "无水印特征不误删",
			in:   "https://v.douyinvod.com/a.mp4?lr=video_gen_no_watermark",
			want: "https://v.douyinvod.com/a.mp4?lr=video_gen_no_watermark",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanVideoURL(c.in); got != c.want {
				t.Fatalf("cleanVideoURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestExtractVidFromURL(t *testing.T) {
	cases := map[string]string{
		"https://v.douyinvod.com/video/tos/cn/v0d00fg1000abcdef12345678/?a=1": "v0d00fg1000abcdef12345678",
		"https://v.douyinvod.com/a.mp4?vid=v0d00fg1000abcdef123456&w=1":       "v0d00fg1000abcdef123456",
		"https://v.douyinvod.com/a.mp4?video_id=v0d00fg1000abcdef123456":      "v0d00fg1000abcdef123456",
		"https://v.douyinvod.com/a.mp4?novid=1":                               "",
	}
	for in, want := range cases {
		if got := extractVidFromURL(in); got != want {
			t.Errorf("extractVidFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChooseBestVideoURL(t *testing.T) {
	cands := []videoCandidate{
		{key: "main_url", source: "data", url: "https://cdn/a.mp4?watermark=1&logo=x"},
		{key: "main_url", source: "original_media_info", url: "https://cdn/original.mp4?lr=video_gen_no_watermark", width: 1080, height: 1920},
		{key: "url", source: "play_info", url: "https://cdn/play.mp4"},
	}
	if got := chooseBestVideoURL(cands); got != cands[1].url {
		t.Fatalf("应选择 original_media_info 的无水印地址，实际 %q", got)
	}

	// 全部带水印时应选「污染最轻」的，而不是直接丢弃
	allMarked := []videoCandidate{
		{key: "main_url", source: "data", url: "https://cdn/a.mp4?watermark=1"},
		{key: "url", source: "play_info", url: "https://cdn/b.mp4"},
	}
	if got := chooseBestVideoURL(allMarked); got != allMarked[1].url {
		t.Fatalf("无水印特征者应胜出，实际 %q", got)
	}

	if got := chooseBestVideoURL(nil); got != "" {
		t.Fatalf("空候选应返回空串，实际 %q", got)
	}
}

func TestScoreWatermarkPenalty(t *testing.T) {
	clean := scoreVideoCandidate(videoCandidate{url: "https://cdn/a.mp4"})
	marked := scoreVideoCandidate(videoCandidate{url: "https://cdn/a.mp4?watermark=1&logo=x"})
	if marked >= clean {
		t.Fatalf("带水印地址得分(%d)应低于干净地址(%d)", marked, clean)
	}
	if isLikelyMarkedURL("https://cdn/a.mp4?lr=video_gen_no_watermark", "") {
		t.Fatal("video_gen_no_watermark 不应被判为带水印")
	}
}

func TestResolveFallbackKeepsOriginal(t *testing.T) {
	// 无登录态（空 Cookie）时不应改动结果——直接保留原地址。
	// resolveNoWatermarkVideoURL 在 vid 提取/接口失败时也应回落原地址。
	in := "https://v.douyinvod.com/a.mp4?watermark=1"
	if got := cleanVideoURL(in); got == "" {
		t.Fatal("清洗结果不应为空")
	}
}

// 回归：豆包 get_play_info 的宽高会在数字/字符串之间摇摆（如 meta.height="1080"），
// 曾导致整个响应反序列化失败、候选列表为空、无水印升级静默退化为 URL 改写。
func TestFlexIntToleratesStringNumbers(t *testing.T) {
	const payload = `{"code":0,"data":{"original_media_info":{"main_url":"https://cdn/original.mp4","width":"720","height":"1280","meta":{"width":"720","height":"1280"}}}}`
	cands, err := fetchPlayInfoCandidatesFromJSON([]byte(payload))
	if err != nil {
		t.Fatalf("fetchPlayInfoCandidatesFromJSON: %v", err)
	}
	if len(cands) == 0 {
		t.Fatal("期望解析出 original_media_info 候选")
	}
	if cands[0].width != 720 || cands[0].height != 1280 {
		t.Fatalf("宽高应为 720x1280，得到 %dx%d", cands[0].width, cands[0].height)
	}
}
