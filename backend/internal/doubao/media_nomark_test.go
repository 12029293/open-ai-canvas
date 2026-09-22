package doubao

import (
	"testing"
)

// ---------------------------------------------------------------- 图片去水印

func TestCleanImageURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "剥掉 tplv 水印模板段",
			in:   "https://p3.douyinpic.com/img/x~tplv-obj-watermark.image?sign=1",
			want: "https://p3.douyinpic.com/img/x.image?sign=1",
		},
		{
			name: "删除 logo 参数",
			in:   "https://a.com/img.png?x=1&logo=wm&y=2",
			want: "https://a.com/img.png?x=1&y=2",
		},
		{
			name: "干净地址原样保留",
			in:   "https://p3.douyinpic.com/obj/tos-cn/img.png?x=1",
			want: "https://p3.douyinpic.com/obj/tos-cn/img.png?x=1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanImageURL(c.in); got != c.want {
				t.Fatalf("cleanImageURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// image_ori_raw 优先：同一 item 里即使带水印缩略图在前，也应取无水印原图。
func TestCollectImagesPrefersImageOriRaw(t *testing.T) {
	rawURL := "https://p3-sign.douyinpic.com/tos-cn/img_raw.png?x=1"
	item := map[string]any{
		"key": "k1",
		"image_thumb": map[string]any{
			"url": "https://p3-sign.douyinpic.com/tos-cn/img_raw~tplv-xj-mark-watermark.png?x=1",
		},
		"image_ori_raw": map[string]any{
			"url": rawURL, "width": 1024, "height": 1024,
		},
	}
	ev := SSEEvent{Data: map[string]any{
		"event_type": float64(2001),
		"event_data": map[string]any{"message": map[string]any{
			"content_type": float64(2010),
			"content":      map[string]any{"data": []any{item}},
		}},
	}}
	images := CollectImages([]SSEEvent{ev})
	if len(images) != 1 {
		t.Fatalf("应恰好收集 1 张图，实际 %v", images)
	}
	if images[0] != rawURL {
		t.Fatalf("应优先取 image_ori_raw 原图，实际 %q", images[0])
	}
}

// patch_op / creation_block 补丁流里的 creations 也要能提取 image_ori_raw
// （对齐豆包前端插件 harvest 的扫描面），不受 event_type 门控限制。
func TestCollectImagesFromCreationBlockPatch(t *testing.T) {
	rawURL := "https://p3-sign.douyinpic.com/tos-cn/patch_raw.png?x=1"
	ev := SSEEvent{Data: map[string]any{
		"patch_op": []any{map[string]any{
			"patch_value": map[string]any{
				"content_block": []any{map[string]any{
					"content": map[string]any{
						"creation_block": map[string]any{
							"creations": []any{map[string]any{
								"image": map[string]any{
									"key":           "k2",
									"image_ori_raw": map[string]any{"url": rawURL},
								},
							}},
						},
					},
				}},
			},
		}},
	}}
	images := CollectImages([]SSEEvent{ev})
	if len(images) != 1 || images[0] != rawURL {
		t.Fatalf("补丁流应提取出 image_ori_raw 原图，实际 %v", images)
	}
}

// ---------------------------------------------------------------- 视频去水印

// get_download_info 响应结构未知字段多，宽容遍历应能从嵌套 video_list 里
// 收集候选并按无水印特征择优。
func TestCandidatesFromMediaJSONDownloadInfo(t *testing.T) {
	payload := []byte(`{
		"code": 0,
		"data": {
			"download_info": {
				"video_list": {
					"video_1": {"main_url": "https://v.douyinvod.com/src/marked.mp4?lr=video_gen_watermark", "vwidth": "720", "vheight": "1280"},
					"video_2": {"main_url": "https://v.douyinvod.com/src/clean.mp4?lr=video_gen_no_watermark"}
				}
			}
		}
	}`)
	cands := candidatesFromMediaJSON(payload)
	if len(cands) != 2 {
		t.Fatalf("应解析出 2 个候选，实际 %d 个：%v", len(cands), cands)
	}
	best := chooseBestVideoURL(cands)
	if best != "https://v.douyinvod.com/src/clean.mp4?lr=video_gen_no_watermark" {
		t.Fatalf("应择优无水印候选，实际 %q", best)
	}
	for _, c := range cands {
		if c.source != "download_info" {
			t.Fatalf("download_info 内嵌节点应带 download_info 来源标记，实际 %q", c.source)
		}
	}
}

func TestCandidatesFromMediaJSONSkipsNonVideoURLs(t *testing.T) {
	payload := []byte(`{"code":0,"data":{"cover_url":"https://p3.douyinpic.com/cover.png","download_url":"https://v.douyinvod.com/a.mp4?x=1"}}`)
	cands := candidatesFromMediaJSON(payload)
	if len(cands) != 1 {
		t.Fatalf("图片封面不应混入视频候选，实际 %v", cands)
	}
}

func TestHasUnmarkedCandidate(t *testing.T) {
	marked := []videoCandidate{{key: "main_url", source: "data", url: "https://cdn/a.mp4?watermark=1"}}
	if hasUnmarkedCandidate(marked) {
		t.Fatal("全部带水印特征时应返回 false")
	}
	mixed := append(marked, videoCandidate{key: "main_url", source: "data", url: "https://cdn/b.mp4"})
	if !hasUnmarkedCandidate(mixed) {
		t.Fatal("存在干净候选时应返回 true")
	}
}
