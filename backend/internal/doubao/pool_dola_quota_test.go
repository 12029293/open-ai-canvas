package doubao

// Dola 每日视频配额与站点绑定取号回归：
// - dola- 前缀模型（Site=dola）只在 Dola 站点取号，不跨站兜底；
// - Dola 账号每日 2 条视频配额，记满后当日跳过，归属日跨天后自动恢复。

import (
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

func TestPickSiteDolaBoundDoesNotFallBackToDoubao(t *testing.T) {
	s := newPoolTestService(t)
	if _, err := s.Upsert(UpsertInput{Cookie: "aaaaaaaaaaaaaaaaaaaa", Site: SiteDoubao, Label: "豆包可用"}); err != nil {
		t.Fatal(err)
	}
	// Dola 站点没有可用账号时，站点绑定取号必须失败，而不是落到豆包账号。
	if _, err := s.PickSite(SiteDola, ""); err == nil {
		t.Fatal("Dola 站点无账号时 PickSite(dola) 应报错，不能跨站取豆包账号")
	} else if !strings.Contains(err.Error(), "Dola") {
		t.Fatalf("错误信息应指明 Dola 站点，got %q", err.Error())
	}
}

func TestDolaDailyVideoQuota(t *testing.T) {
	s := newPoolTestService(t)
	if _, err := s.Upsert(UpsertInput{Cookie: "bbbbbbbbbbbbbbbbbbbb", Site: SiteDola, Label: "Dola配额"}); err != nil {
		t.Fatal(err)
	}
	// 当日累计到上限：每次出片后计数 +1（取号即占用，任务结束需释放）。
	for i := 1; i <= dolaDailyVideoQuota; i++ {
		cred, err := s.PickSite(SiteDola, "")
		if err != nil {
			t.Fatalf("第 %d 次取号应可用：%v", i, err)
		}
		if err := s.NoteDolaVideoSuccess(cred.ID); err != nil {
			t.Fatal(err)
		}
		s.ReleaseAccount(cred.ID)
	}
	if _, err := s.PickSite(SiteDola, ""); err == nil {
		t.Fatal("当日配额用尽后 PickSite(dola) 应报错")
	} else if !strings.Contains(err.Error(), "每日 2 条") {
		t.Fatalf("配额错误信息应说明每日 2 条，got %q", err.Error())
	}

	// 归属日翻到昨天之外（模拟跨天）：计数视为 0，恢复可用。
	today := time.Now().Format(quotaDateFormat)
	past := time.Now().AddDate(0, 0, -1).Format(quotaDateFormat)
	if err := s.db.Model(&model.DoubaoAccount{}).Where("site = ?", SiteDola).
		Updates(map[string]any{"video_quota_date": past}).Error; err != nil {
		t.Fatal(err)
	}
	if today == past {
		t.Skip("跨天模拟依赖日期变化")
	}
	cred, err := s.PickSite(SiteDola, "")
	if err != nil {
		t.Fatalf("跨天后配额应自动恢复：%v", err)
	}
	if cred.Site != SiteDola {
		t.Fatalf("应返回 Dola 账号，got site=%q", cred.Site)
	}
}

func TestDolaQuotaIgnoresStaleDate(t *testing.T) {
	// 计数残留但归属日不是今天：dolaQuotaUsed 必须按 0 处理。
	a := &model.DoubaoAccount{VideoCountUsed: dolaDailyVideoQuota, VideoQuotaDate: "2000-01-01"}
	if got := dolaQuotaUsed(a, time.Now().Format(quotaDateFormat)); got != 0 {
		t.Fatalf("跨天残留计数应视为 0，got %d", got)
	}
	a.VideoQuotaDate = time.Now().Format(quotaDateFormat)
	if got := dolaQuotaUsed(a, a.VideoQuotaDate); got != dolaDailyVideoQuota {
		t.Fatalf("当日计数应保留，got %d", got)
	}
}
