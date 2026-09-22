package doubao

// 每日零点额度重置回归：额度耗尽账号跨天恢复可用；登录态失效不受影响。

import (
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

func TestResetQuotaExhaustedKeepsLoginExpired(t *testing.T) {
	s := newPoolTestService(t)

	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteDoubao, Label: "额度耗尽号"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(UpsertInput{Cookie: "bbbbbbbbbbbbbbbbbbbb", Site: SiteDoubao, Label: "登录失效号"}); err != nil {
		t.Fatal(err)
	}

	// 一个额度耗尽（带冷却），一个登录失效（带残留 last_error）。
	quotaAcc := model.DoubaoAccount{}
	if err := s.db.Where("label = ?", "额度耗尽号").First(&quotaAcc).Error; err != nil {
		t.Fatal(err)
	}
	cooldown := time.Now().Add(24 * time.Hour)
	exhaustedAt := time.Now().Add(-time.Hour)
	quotaAcc.QuotaExhaustedAt = &exhaustedAt
	quotaAcc.CooldownUntil = &cooldown
	quotaAcc.LastError = "额度用完了"
	if err := s.db.Save(&quotaAcc).Error; err != nil {
		t.Fatal(err)
	}
	expiredAcc := model.DoubaoAccount{}
	if err := s.db.Where("label = ?", "登录失效号").First(&expiredAcc).Error; err != nil {
		t.Fatal(err)
	}
	expiredAcc.LoginExpired = true
	expiredAcc.LastError = "登录态失效"
	if err := s.db.Save(&expiredAcc).Error; err != nil {
		t.Fatal(err)
	}

	n, err := s.ResetQuotaExhausted()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应只重置 1 个额度耗尽账号，实际 %d", n)
	}

	// 用全新结构体重载：GORM 扫描不会把 NULL 列覆盖进已填充的指针字段。
	var reloadedQuota model.DoubaoAccount
	if err := s.db.First(&reloadedQuota, "id = ?", quotaAcc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedQuota.QuotaExhaustedAt != nil || reloadedQuota.CooldownUntil != nil || reloadedQuota.LastError != "" {
		t.Fatalf("额度耗尽账号未被重置: %+v", reloadedQuota)
	}
	var reloadedExpired model.DoubaoAccount
	if err := s.db.First(&reloadedExpired, "id = ?", expiredAcc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reloadedExpired.LoginExpired || reloadedExpired.LastError == "" {
		t.Fatalf("登录失效账号不应被额度重置影响: %+v", reloadedExpired)
	}

	// 重置后账号应可被取号（usable 语义）。
	now := time.Now()
	if !s.usable(&reloadedQuota, now) {
		t.Fatal("重置后的额度耗尽账号应恢复可用")
	}
	if s.usable(&reloadedExpired, now) {
		t.Fatal("登录失效账号仍应不可用")
	}
}

func TestResetQuotaExhaustedBeforeKeepsTodayFlags(t *testing.T) {
	s := newPoolTestService(t)
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteDoubao}); err != nil {
		t.Fatal(err)
	}
	acc := model.DoubaoAccount{}
	if err := s.db.First(&acc).Error; err != nil {
		t.Fatal(err)
	}
	today := time.Now()
	yesterday := today.AddDate(0, 0, -1)

	acc.QuotaExhaustedAt = &yesterday
	if err := s.db.Save(&acc).Error; err != nil {
		t.Fatal(err)
	}
	nowDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	if n, err := s.ResetQuotaExhaustedBefore(nowDate); err != nil || n != 1 {
		t.Fatalf("昨天标记的额度耗尽应被跨天重置 n=%d err=%v", n, err)
	}

	acc.QuotaExhaustedAt = &today
	if err := s.db.Save(&acc).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := s.ResetQuotaExhaustedBefore(nowDate); err != nil || n != 0 {
		t.Fatalf("当天标记的额度耗尽不应被跨天补偿重置 n=%d err=%v", n, err)
	}
}
