package doubao

// 账号池每日额度重置：额度耗尽（quota_exhausted）的账号每天零点自动清除
// 额度标记与关联冷却，恢复可用；跨天重置不区分豆包 / Dola / 即梦站点。
// 登录态失效（LoginExpired）不属于额度问题，不在此重置，仍需重新登录。

import (
	"context"
	"log"
	"time"

	"infinite-canvas/backend/internal/model"
)

// StartQuotaDailyReset 启动每日零点额度重置循环（本地时区 00:00）。
// 启动时先补一次跨天重置：服务停机跨过零点时，停机前标记的额度耗尽账号
// 也会在下次启动时恢复，避免「昨晚耗尽、今早仍不可用」。
func (s *Service) StartQuotaDailyReset(ctx context.Context) {
	go s.runQuotaResetLoop(ctx)
}

func (s *Service) runQuotaResetLoop(ctx context.Context) {
	// 显式构造本地时区当天零点（不用 Truncate，其按 UTC 天截断）。
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if n, err := s.ResetQuotaExhaustedBefore(todayStart); err != nil {
		log.Printf("[doubao] 账号池启动跨天额度重置失败: %v", err)
	} else if n > 0 {
		log.Printf("[doubao] 账号池启动跨天额度重置: 恢复 %d 个额度耗尽账号", n)
	}
	for {
		now := time.Now()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
		timer := time.NewTimer(nextMidnight.Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if n, err := s.ResetQuotaExhausted(); err != nil {
			log.Printf("[doubao] 每日零点额度重置失败: %v", err)
		} else if n > 0 {
			log.Printf("[doubao] 每日零点额度重置: 恢复 %d 个额度耗尽账号", n)
		}
	}
}

// ResetQuotaExhausted 清除全部额度耗尽标记与关联冷却（每日零点重置调用）。
func (s *Service) ResetQuotaExhausted() (int64, error) {
	return s.resetQuotaExhaustedWhere("quota_exhausted_at IS NOT NULL")
}

// ResetQuotaExhaustedBefore 只重置在 before 之前标记额度耗尽的账号
// （启动补偿用：当天新标记的额度耗尽保留原冷却）。
func (s *Service) ResetQuotaExhaustedBefore(before time.Time) (int64, error) {
	return s.resetQuotaExhaustedWhere("quota_exhausted_at IS NOT NULL AND quota_exhausted_at < ?", before)
}

func (s *Service) resetQuotaExhaustedWhere(query string, args ...any) (int64, error) {
	if s.db == nil {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res := s.db.Model(&model.DoubaoAccount{}).
		Where(query, args...).
		Updates(map[string]any{
			"cooldown_until":     nil,
			"quota_exhausted_at": nil,
			"last_error":         "",
			"updated_at":         time.Now(),
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
