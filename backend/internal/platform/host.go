package platform

import (
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// TaskWorkerConcurrency 全局任务 worker 默认并发。
// 原为 3：账号池场景下可用账号数往往大于 3，worker 会先于渠道并发成为瓶颈
//（其余任务全部卡在「排队中」）。放大默认值，让并发主要由「渠道并发 = 账号数」
// 决定；部署方仍可用 CANVAS_WORKER_CONCURRENCY 或运行时策略覆盖。
const TaskWorkerConcurrency = 16

// Host 由组合根注入，避免 platform → app 回环。
type Host interface {
	RequireAdmin(user *model.User) error
	AppendAudit(actor *model.User, action, targetType, targetID, summary string, metadata any) error
	ChannelConcurrencyLimit(channelID string) (int, error)
}

type nopHost struct{}

func (nopHost) RequireAdmin(*model.User) error { return nil }
func (nopHost) AppendAudit(*model.User, string, string, string, string, any) error {
	return nil
}
func (nopHost) ChannelConcurrencyLimit(string) (int, error) {
	return 0, nil
}

type Service struct {
	repo             *repository.Repository
	host             Host
	Coordinator      *Coordinator
	concurrencyCache *BoundedReadCache[string, RuntimeTaskPolicy]
}

func New(repo *repository.Repository, coordinator *Coordinator, host Host) *Service {
	if host == nil {
		host = nopHost{}
	}
	return &Service{
		repo:             repo,
		host:             host,
		Coordinator:      coordinator,
		concurrencyCache: NewBoundedReadCache[string, RuntimeTaskPolicy](1, 1024, 1, 2*time.Second),
	}
}

func (s *Service) requireAdmin(user *model.User) error {
	if s == nil || s.host == nil {
		return kernel.Unauthorized("请先登录")
	}
	return s.host.RequireAdmin(user)
}

func (s *Service) appendAdminAudit(actor *model.User, action, targetType, targetID, summary string, metadata any) error {
	if s == nil || s.host == nil {
		return nil
	}
	return s.host.AppendAudit(actor, action, targetType, targetID, summary, metadata)
}
