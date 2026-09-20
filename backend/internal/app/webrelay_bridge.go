package app

// 网页中继账号池：app 层对 webrelay 域的桥接（handler 只 import service 别名）。

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"infinite-canvas/backend/internal/webrelay"

	"gorm.io/gorm"
)

func (s *Service) webRelayPool() *webrelay.Service {
	s.webrelayMu.Lock()
	defer s.webrelayMu.Unlock()
	return s.webRelayPoolLocked()
}

// webRelayPoolLocked 需持有 webrelayMu（sync.Mutex 不可重入，内部复用必须走此入口）。
func (s *Service) webRelayPoolLocked() *webrelay.Service {
	if s.webrelaySvc == nil {
		var db *gorm.DB
		if s.repo != nil {
			db = s.repo.DB()
		}
		s.webrelaySvc = webrelay.NewService(db)
	}
	return s.webrelaySvc
}

func (s *Service) WebRelayPoolStatus(site string) (*webrelay.PoolStatus, error) {
	return s.webRelayPool().Status(site)
}

type WebRelayUpsertRequest struct {
	Site   string `json:"site"`
	Token  string `json:"token"`
	Cookie string `json:"cookie"`
	Label  string `json:"label"`
	Note   string `json:"note"`
}

func (s *Service) WebRelayUpsertAccount(req WebRelayUpsertRequest) (*webrelay.AccountView, error) {
	return s.webRelayPool().Upsert(webrelay.UpsertInput{
		Site: req.Site, Token: req.Token, Cookie: req.Cookie,
		Label: req.Label, Note: req.Note, Source: "manual",
	})
}

type WebRelayUpdateRequest struct {
	Label   *string `json:"label"`
	Note    *string `json:"note"`
	Enabled *bool   `json:"enabled"`
	ProxyID *string `json:"proxyId"`
}

func (s *Service) WebRelayUpdateAccount(id string, req WebRelayUpdateRequest) (*webrelay.AccountView, error) {
	return s.webRelayPool().Update(id, webrelay.UpdatePatch{Label: req.Label, Note: req.Note, Enabled: req.Enabled, ProxyID: req.ProxyID})
}

func (s *Service) WebRelayRemoveAccount(id string) error {
	return s.webRelayPool().Remove(id)
}

type WebRelayBatchRequest struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids"`
}

func (s *Service) WebRelayBatchOp(req WebRelayBatchRequest) (int, error) {
	return s.webRelayPool().BatchOp(req.Action, req.IDs)
}

func (s *Service) WebRelayClearAllCooldowns(site string) (int, error) {
	return s.webRelayPool().ClearAllCooldowns(site)
}

func (s *Service) WebRelayPick(site string) (*webrelay.Credential, error) {
	return s.webRelayPool().Pick(site)
}

func (s *Service) WebRelayMarkSuccess(id string) error {
	return s.webRelayPool().MarkSuccess(id)
}

func (s *Service) WebRelayMarkFailed(id string, opts webrelay.MarkFailedOptions) error {
	return s.webRelayPool().MarkFailed(id, opts)
}

type WebRelayCaptureRequest struct {
	Site       string `json:"site"`
	Credential string `json:"credential"`
}

// WebRelayCaptureCredential 书签脚本一键导入：凭据进入内置账号池。
// 池的 Upsert 自带归一化（千问整段 Cookie 提取 token 并保留全量 Cookie）与去重
// （重复导入视为登录态恢复，自动清掉过期/报错标记）。
func (s *Service) WebRelayCaptureCredential(req WebRelayCaptureRequest) (map[string]interface{}, error) {
	credential := strings.TrimSpace(req.Credential)
	if credential == "" {
		return nil, errors.New("凭据为空，请确认已在网页版登录")
	}
	site := req.Site
	normalizedToken := webrelay.NormalizeToken(site, credential)
	duplicate, dupErr := s.webRelayPool().HasToken(site, normalizedToken)
	if dupErr != nil {
		return nil, dupErr
	}
	view, err := s.webRelayPool().Upsert(webrelay.UpsertInput{
		Site: site, Token: credential, Cookie: credential,
		Label: "一键导入 " + time.Now().Format("01-02 15:04"), Source: "capture",
	})
	if err != nil {
		return nil, err
	}
	total := 0
	if status, statusErr := s.webRelayPool().Status(view.Site); statusErr == nil && status != nil {
		total = status.AccountCount
	}
	message := "导入成功，影策账号池现在有 " + strconv.Itoa(total) + " 个凭据"
	if duplicate {
		message = "该凭据已导入过，登录态已刷新"
	}
	return map[string]interface{}{
		"ok": true, "duplicate": duplicate, "total": total,
		"site": view.Site, "label": view.Label, "message": message,
	}, nil
}

type WebRelayTestRequest struct {
	InterfaceType string `json:"interfaceType"`
	Tokens        string `json:"tokens"`
	Model         string `json:"model"`
}

// WebRelayTestConnection 用给定凭据发一条极小的测试消息，验证网页版 Token 是否可用。
// 逐个 token 尝试，任一成功即通过；登录失效/限流自动尝试下一个，其余错误立即返回。
func (s *Service) WebRelayTestConnection(req WebRelayTestRequest) (map[string]interface{}, error) {
	site, err := webRelaySiteForInterface(req.InterfaceType)
	if err != nil {
		return nil, err
	}
	credentials := webRelayDirectCredentials(site, req.Tokens)
	if len(credentials) == 0 {
		// 未填直连 Token：回退用内置账号池里的凭据测试。
		picked, pickErr := s.WebRelayPick(site)
		if pickErr != nil {
			return nil, errors.New("没有可测试的凭据：请先粘贴 Token，或用「一键导入」把凭据存入账号池")
		}
		credentials = []webrelay.Credential{*picked}
	}
	modelKey := strings.TrimSpace(req.Model)
	if modelKey == "" {
		if list := webRelayModels[site]; len(list) > 0 {
			modelKey = list[0].Key
		}
	}
	request := webrelay.ChatRequest{Model: modelKey, Prompt: "连接测试：请只回复两个字——成功"}
	handlers := webrelay.StreamHandlers{
		OnText:      func(string) {},
		OnReasoning: func(string) {},
	}
	// 千问走 CDP 浏览器 UI 中继（凭据直连会被阿里云 WAF 的 RGV587 拦截）。
	if site == webrelay.SiteQwen {
		start := time.Now()
		result, generateErr := s.qwenChatViaCDP(context.Background(), request, handlers)
		latency := time.Since(start).Milliseconds()
		if generateErr != nil {
			return map[string]interface{}{"ok": false, "tokenCount": 0, "message": generateErr.Error()}, nil
		}
		reply := result.Text
		if runes := []rune(reply); len(runes) > 120 {
			reply = string(runes[:120]) + "…"
		}
		return map[string]interface{}{
			"ok": true, "latencyMs": latency, "tokenIndex": 1,
			"tokenCount": 1, "reply": reply,
		}, nil
	}
	var lastErr error
	for index, credential := range credentials {
		start := time.Now()
		result, generateErr := webrelay.GenerateChat(context.Background(), credential, request, handlers)
		latency := time.Since(start).Milliseconds()
		if generateErr == nil {
			reply := result.Text
			if runes := []rune(reply); len(runes) > 120 {
				reply = string(runes[:120]) + "…"
			}
			return map[string]interface{}{
				"ok": true, "latencyMs": latency, "tokenIndex": index + 1,
				"tokenCount": len(credentials), "reply": reply,
			}, nil
		}
		lastErr = generateErr
		switch webrelay.ErrorKind(generateErr) {
		case webrelay.FailKindSessionExpired, webrelay.FailKindRateLimited, webrelay.FailKindQuotaExhausted:
			continue
		default:
			return map[string]interface{}{"ok": false, "tokenIndex": index + 1, "tokenCount": len(credentials), "message": generateErr.Error()}, nil
		}
	}
	return map[string]interface{}{"ok": false, "tokenCount": len(credentials), "message": lastErr.Error()}, nil
}
