package app

// 豆包账号池：app 层对 doubao 域的桥接（handler 只 import service 别名）。

import (
	"context"

	"infinite-canvas/backend/internal/doubao"

	"gorm.io/gorm"
)

func (s *Service) doubaoPool() *doubao.Service {
	s.doubaoMu.Lock()
	defer s.doubaoMu.Unlock()
	return s.doubaoPoolLocked()
}

// doubaoPoolLocked 需持有 doubaoMu（sync.Mutex 不可重入，内部复用必须走此入口）。
func (s *Service) doubaoPoolLocked() *doubao.Service {
	if s.doubaoPoolSvc == nil {
		var db *gorm.DB
		if s.repo != nil {
			db = s.repo.DB()
		}
		s.doubaoPoolSvc = doubao.NewService(db)
	}
	return s.doubaoPoolSvc
}

// doubaoQrLogin 扫码登录管理器（按站点单例，随池惰性创建）。
func (s *Service) doubaoQrLogin(site string) *doubao.QRLoginManager {
	site = doubao.NormalizeSite(site)
	s.doubaoMu.Lock()
	defer s.doubaoMu.Unlock()
	if s.doubaoQrSvcs == nil {
		s.doubaoQrSvcs = map[string]*doubao.QRLoginManager{}
	}
	mgr := s.doubaoQrSvcs[site]
	if mgr == nil {
		mgr = doubao.NewQRLoginManager(s.doubaoPoolLocked(), site)
		s.doubaoQrSvcs[site] = mgr
	}
	return mgr
}

func (s *Service) DoubaoQrStart(site string) (doubao.QRLoginView, bool) {
	return s.doubaoQrLogin(site).Start()
}

func (s *Service) DoubaoQrStatus(site string) doubao.QRLoginView {
	return s.doubaoQrLogin(site).Status()
}

func (s *Service) DoubaoQrCancel(site string) error {
	return s.doubaoQrLogin(site).Cancel()
}

func (s *Service) DoubaoPoolStatus(site string) (*doubao.PoolStatus, error) {
	return s.doubaoPool().Status(site)
}

type DoubaoUpsertRequest struct {
	Cookie    string   `json:"cookie"`
	Site      string   `json:"site"`
	Label     string   `json:"label"`
	Source    string   `json:"source"`
	Tags      []string `json:"tags"`
	Note      string   `json:"note"`
	SetActive bool     `json:"setActive"`
}

func (s *Service) DoubaoUpsertAccount(req DoubaoUpsertRequest) (*doubao.AccountView, error) {
	return s.doubaoPool().Upsert(doubao.UpsertInput{
		Cookie: req.Cookie, Site: req.Site, Label: req.Label, Source: req.Source,
		Tags: req.Tags, Note: req.Note, SetActive: req.SetActive,
	})
}

type DoubaoBulkImportRequest struct {
	Text      string   `json:"text"`
	Site      string   `json:"site"`
	Tags      []string `json:"tags"`
	SetActive bool     `json:"setActive"`
}

func (s *Service) DoubaoBulkImport(req DoubaoBulkImportRequest) (*doubao.BulkImportResult, error) {
	return s.doubaoPool().BulkImport(doubao.BulkImportInput{Text: req.Text, Site: req.Site, Tags: req.Tags, SetActive: req.SetActive})
}

type DoubaoUpdateRequest struct {
	Label   *string   `json:"label"`
	Note    *string   `json:"note"`
	Tags    *[]string `json:"tags"`
	Enabled *bool     `json:"enabled"`
}

func (s *Service) DoubaoUpdateAccount(id string, req DoubaoUpdateRequest) (*doubao.AccountView, error) {
	return s.doubaoPool().Update(id, doubao.UpdatePatch{Label: req.Label, Note: req.Note, Tags: req.Tags, Enabled: req.Enabled})
}

func (s *Service) DoubaoRemoveAccount(id string) error {
	return s.doubaoPool().Remove(id)
}

type DoubaoBatchRequest struct {
	Action string   `json:"action"`
	Site   string   `json:"site"`
	IDs    []string `json:"ids"`
	Tags   []string `json:"tags"`
}

func (s *Service) DoubaoBatchOp(req DoubaoBatchRequest) (int, error) {
	return s.doubaoPool().BatchOp(req.Action, req.Site, req.IDs, req.Tags)
}

func (s *Service) DoubaoPickAccount(preferID string) (*doubao.ActiveCredential, error) {
	return s.doubaoPool().Pick(preferID)
}

func (s *Service) DoubaoMarkSuccess(id string) error {
	return s.doubaoPool().MarkSuccess(id)
}

type DoubaoMarkFailedRequest struct {
	Kind       string `json:"kind"`
	Message    string `json:"message"`
	CooldownMs int64  `json:"cooldownMs"`
}

func (s *Service) DoubaoMarkFailed(id string, req DoubaoMarkFailedRequest) (bool, *doubao.AccountView, error) {
	return s.doubaoPool().MarkFailed(id, doubao.MarkFailedOptions{Kind: req.Kind, Message: req.Message, CooldownMs: req.CooldownMs})
}

func (s *Service) DoubaoClearAllCooldowns() (int, error) {
	return s.doubaoPool().ClearAllCooldowns()
}

type DoubaoGenerateImageRequest struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	Ratio  string `json:"ratio"`
	Style  string `json:"style"`
}

func (s *Service) DoubaoGenerateImage(ctx context.Context, req DoubaoGenerateImageRequest) (*doubao.ImageResult, error) {
	return doubao.GenerateImageWithPool(ctx, s.doubaoPool(), doubao.ImageRequest{
		Prompt: req.Prompt, Model: req.Model, Ratio: req.Ratio, Style: req.Style,
	})
}

type DoubaoGenerateVideoRequest struct {
	Prompt   string `json:"prompt"`
	Model    string `json:"model"`
	Duration int    `json:"duration"`
	Ratio    string `json:"ratio"`
	// Site 站点偏好：dola 表示只用 Dola 账号（dola- 前缀模型），空 = 豆包优先。
	Site string `json:"site"`
}

func (s *Service) DoubaoGenerateVideo(ctx context.Context, req DoubaoGenerateVideoRequest) (*doubao.VideoResult, error) {
	return doubao.GenerateVideoWithPool(ctx, s.doubaoPool(), doubao.VideoRequest{
		Prompt: req.Prompt, Model: req.Model, Duration: req.Duration, Ratio: req.Ratio, Site: req.Site,
	})
}
