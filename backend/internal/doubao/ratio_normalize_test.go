package doubao

import "testing"

// 画布尺寸选择器可能保存像素尺寸，豆包正文只认标准比例；锁定归一规则防回归。
func TestNormalizeDoubaoImageRatio(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"auto", "auto"},
		{"16:9", "16:9"},        // 比例原样透传
		{"9:16", "9:16"},        // 含大写风险：toLower 后仍为比例
		{"1824x1024", "16:9"},   // 1K 横屏像素档 → 16:9
		{"1024x1824", "9:16"},   // 1K 竖屏像素档 → 9:16
		{"1024x1024", "1:1"},    // 方形
		{"1536x1024", "3:2"},    // 3:2 标准档
		{"2048x878", "21:9"},    // 21:9 标准档
		{"3840x2160", "16:9"},   // 4K 档
		{"1360x1024", "4:3"},    // 4:3 标准档
		{"1075x610", "16:9"},    // 非标准像素但比例在 2% 容差内
		{"100x333", "100:333"},  // 无标准命中按 GCD 简化（GCD=1 原样）
		{"bad", ""},             // 非法输入
		{"12x0", ""},            // 非法高度
	}
	for _, c := range cases {
		if got := normalizeDoubaoImageRatio(c.in); got != c.want {
			t.Errorf("normalizeDoubaoImageRatio(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 锁定方位词文本：像素归一后应产出豆包可读的「横屏 16:9」语义。
func TestRatioSpecTextAfterNormalize(t *testing.T) {
	spec := ratioSpecText(normalizeDoubaoImageRatio("1824x1024"))
	if spec != "横屏 16:9" {
		t.Fatalf("pixel size should map to landscape ratio spec, got %q", spec)
	}
	spec = ratioSpecText(normalizeDoubaoImageRatio("1024x1824"))
	if spec != "竖屏 9:16" {
		t.Fatalf("pixel size should map to portrait ratio spec, got %q", spec)
	}
}
