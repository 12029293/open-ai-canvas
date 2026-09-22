package auth

import (
	"errors"
	"log"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// LocalModeUsername 是本地单机模式内置管理员的固定用户名。
const LocalModeUsername = "local"

// SetLocalMode 开启本地单机模式：CurrentUser 无条件返回内置管理员，
// 登录/注册界面即被整体跳过。仅供桌面单文件版使用；
// 多用户服务端（docker-compose 部署等）必须保持关闭。
func (s *Service) SetLocalMode(enabled bool) {
	if s == nil {
		return
	}
	s.localMode = enabled
}

// LocalMode 返回本地单机模式是否开启。
func (s *Service) LocalMode() bool {
	if s == nil {
		return false
	}
	return s.localMode
}

// localAdminUser 返回内置管理员；首次调用时查找或创建，结果进程内缓存。
// 数据库里的历史账号若被降权/禁用会就地修复，保证本地版始终可用。
func (s *Service) localAdminUser() (*model.User, error) {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	if s.localUser != nil {
		return s.localUser, nil
	}
	user, err := s.repo.UserByUsername(LocalModeUsername)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		passwordHash, hashErr := HashPassword(RandomToken())
		if hashErr != nil {
			return nil, hashErr
		}
		now := time.Now()
		user = &model.User{
			ID:           kernel.NewID(),
			Username:     LocalModeUsername,
			DisplayName:  "本地用户",
			Role:         model.UserRoleAdmin,
			Status:       model.UserStatusActive,
			PasswordHash: passwordHash,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.repo.Create(user); err != nil {
			return nil, err
		}
		if err := s.host.EnsureSignupBonus(user.ID); err != nil {
			return nil, err
		}
		log.Printf("local mode: created built-in admin user %s", LocalModeUsername)
	} else if err != nil {
		return nil, err
	} else if user.Role != model.UserRoleAdmin || user.Status != model.UserStatusActive {
		user.Role = model.UserRoleAdmin
		user.Status = model.UserStatusActive
		user.UpdatedAt = time.Now()
		if err := s.repo.Save(user); err != nil {
			return nil, err
		}
	}
	s.localUser = user
	return user, nil
}
