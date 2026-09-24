package app

// 网络代理管理：账号池账号可绑定出站代理（HTTP / HTTPS / SOCKS5）。
// 接口面与豆包工作台对齐：list / create / update / delete / test / assign。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/outbound"

	"golang.org/x/net/proxy"
	"gorm.io/gorm"
)

// NetworkProxyView 代理的对外视图（不含密码明文）。
type NetworkProxyView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Protocol    string    `json:"protocol"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Username    string    `json:"username,omitempty"`
	HasPassword bool      `json:"hasPassword"`
	Remark      string    `json:"remark"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func proxyView(p *model.NetworkProxy) NetworkProxyView {
	return NetworkProxyView{
		ID: p.ID, Name: p.Name, Protocol: p.Protocol, Host: p.Host, Port: p.Port,
		Username: p.Username, HasPassword: p.Password != "", Remark: p.Remark,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// NetworkProxyUpsertRequest 创建 / 更新代理的入参（更新时 ID 必填）。
type NetworkProxyUpsertRequest struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	// Password 为空且原代理有密码时保留原密码；HasPassword 显式清空密码。
	Password    string `json:"password"`
	ClearPassword bool `json:"clearPassword"`
	Remark      string `json:"remark"`
}

func randomProxyID() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return "npx_" + hex.EncodeToString(buf)
}

func (r *NetworkProxyUpsertRequest) normalize() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errors.New("请填写代理名称")
	}
	if runes := []rune(r.Name); len(runes) > 64 {
		return errors.New("可用代理名称过长")
	}
	r.Protocol = model.NormalizedProxyProtocol(r.Protocol)
	if r.Protocol == "" {
		r.Protocol = model.ProxyProtocolHTTP
	}
	r.Host = strings.TrimSpace(r.Host)
	if r.Host == "" {
		return errors.New("请填写主机地址")
	}
	if r.Port <= 0 || r.Port > 65535 {
		return errors.New("端口无效（1-65535）")
	}
	r.Remark = strings.TrimSpace(r.Remark)
	return nil
}

// NetworkProxyList 返回全部代理（按创建时间倒序）。
func (s *Service) NetworkProxyList() ([]NetworkProxyView, error) {
	var rows []model.NetworkProxy
	if err := s.repo.DB().Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	views := make([]NetworkProxyView, 0, len(rows))
	for i := range rows {
		views = append(views, proxyView(&rows[i]))
	}
	return views, nil
}

// NetworkProxyCreate 新建代理。
func (s *Service) NetworkProxyCreate(req NetworkProxyUpsertRequest) (NetworkProxyView, error) {
	if err := req.normalize(); err != nil {
		return NetworkProxyView{}, err
	}
	row := model.NetworkProxy{
		ID: randomProxyID(), Name: req.Name, Protocol: req.Protocol,
		Host: req.Host, Port: req.Port, Username: strings.TrimSpace(req.Username),
		Password: req.Password, Remark: req.Remark,
	}
	if err := s.repo.DB().Create(&row).Error; err != nil {
		return NetworkProxyView{}, err
	}
	return proxyView(&row), nil
}

// NetworkProxyUpdate 更新代理。
func (s *Service) NetworkProxyUpdate(id string, req NetworkProxyUpsertRequest) (NetworkProxyView, error) {
	if err := req.normalize(); err != nil {
		return NetworkProxyView{}, err
	}
	var row model.NetworkProxy
	if err := s.repo.DB().Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NetworkProxyView{}, errors.New("代理不存在或已删除")
		}
		return NetworkProxyView{}, err
	}
	row.Name, row.Protocol, row.Host, row.Port = req.Name, req.Protocol, req.Host, req.Port
	row.Username = strings.TrimSpace(req.Username)
	switch {
	case req.ClearPassword:
		row.Password = ""
	case req.Password != "":
		row.Password = req.Password
	}
	row.Remark = req.Remark
	if err := s.repo.DB().Save(&row).Error; err != nil {
		return NetworkProxyView{}, err
	}
	return proxyView(&row), nil
}

// NetworkProxyDelete 删除代理并解绑全部账号（回到直连）。
func (s *Service) NetworkProxyDelete(id string) error {
	return s.repo.DB().Transaction(func(tx *gorm.DB) error {
		var row model.NetworkProxy
		if err := tx.Where("id = ?", id).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("代理不存在或已删除")
			}
			return err
		}
		if err := tx.Model(&model.DoubaoAccount{}).
			Where("proxy_id = ?", id).
			Updates(map[string]any{"proxy_id": "", "proxy_url": "", "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.WebRelayAccount{}).
			Where("proxy_id = ?", id).
			Update("proxy_id", "").Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
}

// NetworkProxyTestResult 连通性测试结果。
type NetworkProxyTestResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Message   string `json:"message"`
}

// NetworkProxyTest 通过代理访问公网探针，验证代理可用性。
func (s *Service) NetworkProxyTest(id string) (*NetworkProxyTestResult, error) {
	var row model.NetworkProxy
	if err := s.repo.DB().Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("代理不存在或已删除")
		}
		return nil, err
	}
	result, err := testProxyURL(row.ProxyURL())
	return decorateProxyTestResult(result, row.NeedsDynamicSID()), err
}

// decorateProxyTestResult 为动态轮换代理的测试结果补充行为说明。
func decorateProxyTestResult(result *NetworkProxyTestResult, dynamic bool) *NetworkProxyTestResult {
	if result != nil && result.OK && dynamic {
		result.Message += "（动态轮换代理：每次测试/取号分配新出口 IP）"
	}
	return result
}

// outboundProxyClient 按代理地址构建测试用 HTTP 客户端（支持 http/https/socks5）。
// SOCKS5 及代理拨号均使用并行竞速转发器，代理商多 IP 轮询解析时
// 任一出口可达即可完成测试，避免串行撞死 IP 耗尽整体超时。
func outboundProxyClient(parsed *url.URL) (*http.Client, error) {
	if parsed.Scheme == "socks5" {
		var auth *proxy.Auth
		if parsed.User != nil {
			password, _ := parsed.User.Password()
			auth = &proxy.Auth{User: parsed.User.Username(), Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, outbound.ParallelDialer{})
		if err != nil {
			return nil, fmt.Errorf("SOCKS5 拨号器构建失败：%w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS5 拨号器不支持 context")
		}
		return &http.Client{Transport: &http.Transport{DialContext: contextDialer.DialContext}}, nil
	}
	return &http.Client{Transport: &http.Transport{
		Proxy:       http.ProxyURL(parsed),
		DialContext: outbound.ParallelDialer{}.DialContext,
	}}, nil
}

// testProxyRequestURL 公网探针（选响应快且少被墙内业务拦截的探测地址）。
const testProxyRequestURL = "https://connect.rom.miui.com/generate_204"

func testProxyURL(proxyURL string) (*NetworkProxyTestResult, error) {
	if strings.TrimSpace(proxyURL) == "" {
		return nil, errors.New("代理地址无效")
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("代理地址无效")
	}
	client, err := outboundProxyClient(parsed)
	if err != nil {
		return nil, err
	}
	// 代理隧道（SOCKS5 握手 + TLS）整体耗时较高，给足 20 秒。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testProxyRequestURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		log.Printf("[network-proxy] 测试失败 %.80s: %v", proxyURL, err)
		message := fmt.Sprintf("连接失败：%v", err)
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			message = "连接超时：代理节点未在限时内响应（节点慢、线路不通或凭据未被接受）"
		}
		return &NetworkProxyTestResult{OK: false, Message: message}, nil
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		return &NetworkProxyTestResult{OK: false, LatencyMs: latency, Message: fmt.Sprintf("探针返回 HTTP %d", res.StatusCode)}, nil
	}
	return &NetworkProxyTestResult{OK: true, LatencyMs: latency, Message: fmt.Sprintf("连接测试通过（%d ms）", latency)}, nil
}

// NetworkProxyAssignRequest 给一批账号绑定/解绑代理。
// PoolType 支持 doubao（账号池）与 webrelay（网页中继账号池）；ProxyID 为空 = 全部恢复直连。
type NetworkProxyAssignRequest struct {
	PoolType string   `json:"poolType"`
	IDs      []string `json:"ids"`
	ProxyID  string   `json:"proxyId"`
}

// NetworkProxyAssign 绑定代理到账号，返回受影响的账号数。
func (s *Service) NetworkProxyAssign(req NetworkProxyAssignRequest) (int, error) {
	poolType := strings.TrimSpace(req.PoolType)
	if poolType != "doubao" && poolType != "webrelay" {
		return 0, fmt.Errorf("不支持的账号池类型：%s", req.PoolType)
	}
	proxyID := strings.TrimSpace(req.ProxyID)
	proxyURL := ""
	if proxyID != "" {
		var row model.NetworkProxy
		if err := s.repo.DB().Where("id = ?", proxyID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, errors.New("代理不存在或已删除")
			}
			return 0, err
		}
		proxyURL = row.ProxyURL()
		if proxyURL == "" {
			return 0, errors.New("代理配置不完整")
		}
	}
	if poolType == "webrelay" {
		affected := 0
		for _, id := range req.IDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			result := s.repo.DB().Model(&model.WebRelayAccount{}).Where("id = ?", id).Update("proxy_id", proxyID)
			if result.Error != nil {
				return affected, result.Error
			}
			affected += int(result.RowsAffected)
		}
		return affected, nil
	}
	affected, err := s.doubaoPool().AssignProxy(req.IDs, proxyID, proxyURL)
	return int(affected), err
}
