package doubao

import (
	"strings"
	"testing"
	"time"
)

// 真实上游回复样本（2026-09-21 任务 fae9a361 的会话文本）：提交回复同时含
// 受理话术与周期额度说明，修复前 quotaPatterns 匹配不到、任务白等 20 分钟。
const weeklyQuotaReplySample = "正在为您生成15秒的新娘结婚视频，请稍等片刻。最近视频创作消耗了较多额度，近 7 天的额度用完了，我得休息一阵子了，预计9月28日 17:00 恢复为你服务。升级你的订阅套餐，免等待，继续为你服务。或加购创作额度包，可按需使用，继续生成图片和视频。"

func TestWeeklyQuotaReplyDetectedAsQuota(t *testing.T) {
	if m := quotaPatterns.FindString(weeklyQuotaReplySample); m == "" {
		t.Fatalf("quotaPatterns 应命中周额度回复（额度用完了）")
	} else if !strings.Contains(m, "额度用完") {
		t.Fatalf("命中文案不符: %q", m)
	}
	if !weeklyQuotaPatterns.MatchString(weeklyQuotaReplySample) {
		t.Fatalf("weeklyQuotaPatterns 应命中周期额度回复")
	}
}

func TestWeeklyQuotaSubmitBlockedImmediately(t *testing.T) {
	// 提交回复即含周额度说明：detectBlock 应直接判额度耗尽，不能当作已受理去轮询。
	block := detectBlock(nil, "", weeklyQuotaReplySample)
	if block == nil {
		t.Fatalf("detectBlock 应识别出阻断")
	}
	if block.Kind != FailKindQuotaExhausted {
		t.Fatalf("Kind = %q, want quota_exhausted", block.Kind)
	}
	ce := enrichQuotaCooldown(block, weeklyQuotaReplySample)
	if ce.CooldownUntil.IsZero() {
		t.Fatalf("enrichQuotaCooldown 未设置冷却截止")
	}
	want, _ := weeklyQuotaRecoveryAt(weeklyQuotaReplySample)
	if !ce.CooldownUntil.Equal(want) {
		t.Fatalf("CooldownUntil = %v, want %v", ce.CooldownUntil, want)
	}
	if time.Until(ce.CooldownUntil) <= 0 {
		t.Fatalf("恢复时间应在未来: %v", ce.CooldownUntil)
	}
}

func TestWeeklyQuotaRecoveryAt(t *testing.T) {
	now := time.Now()
	until, ok := weeklyQuotaRecoveryAt("近 7 天的额度用完了，预计9月28日 17:00 恢复为你服务")
	if !ok {
		t.Fatalf("应解析出恢复时间")
	}
	if until.Month() != time.September || until.Day() != 28 || until.Hour() != 17 || until.Minute() != 0 {
		t.Fatalf("解析结果不符: %v", until)
	}
	if until.Before(now) && until.Year() == now.Year() {
		t.Fatalf("恢复时间应晚于现在（必要时跨年）: %v", until)
	}
	if _, ok := weeklyQuotaRecoveryAt("额度用完了，休息一阵子"); ok {
		t.Fatalf("未声明恢复时间时应返回 false")
	}
}

func TestEnrichQuotaCooldownUnrelatedKindsUntouched(t *testing.T) {
	ce := &ClassifyError{Kind: FailKindRateLimited, Message: "上游风控限流"}
	if got := enrichQuotaCooldown(ce, weeklyQuotaReplySample); got != ce || !got.CooldownUntil.IsZero() {
		t.Fatalf("限流错误不应被增强")
	}
	// 无周期额度的普通额度错误不延长冷却
	ce2 := &ClassifyError{Kind: FailKindQuotaExhausted, Message: "额度已用完（今日额度）"}
	if got := enrichQuotaCooldown(ce2, "今日额度已用完"); !got.CooldownUntil.IsZero() {
		t.Fatalf("普通额度错误不应延长冷却")
	}
}
