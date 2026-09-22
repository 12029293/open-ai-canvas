package webrelay

// 中继客户端共享类型与错误分类。

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ChatMessage 文本对话消息（role: system / user / assistant）。
type ChatMessage struct {
	Role    string
	Content string
}

// ChatRequest 文本生成请求（协议中立）。
type ChatRequest struct {
	Model        string // 渠道模型键（如 deepseek-reasoner / qwen3-max-thinking）
	SystemPrompt string
	History      []ChatMessage
	Prompt       string
	Thinking     bool
	// Attachments 多模态附件（当前仅千问网页版支持：图片/视频/文件）。
	Attachments []ChatAttachment
}

// ChatAttachment 随消息上传的多模态附件原始数据。
type ChatAttachment struct {
	Name     string
	MimeType string
	Data     []byte
}

// ChatResult 文本生成结果。
type ChatResult struct {
	Text      string
	Reasoning string
}

// StreamHandlers 流式回调；nil 表示不流式（内部仍走流式接口并聚合）。
type StreamHandlers struct {
	OnText      func(string)
	OnReasoning func(string)
}

// relayError 带失败分类的中继错误。
type relayError struct {
	kind    string
	message string
}

func (e *relayError) Error() string { return e.message }
func (e *relayError) RelayErrorKind() string {
	if e.kind == "" {
		return FailKindRateLimited
	}
	return e.kind
}

func newRelayError(kind string, format string, args ...any) error {
	return &relayError{kind: kind, message: fmt.Sprintf(format, args...)}
}

// classifyHTTPStatus 把上游 HTTP 状态映射为失败分类。
func classifyHTTPStatus(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return FailKindSessionExpired
	case http.StatusTooManyRequests:
		return FailKindRateLimited
	case http.StatusPaymentRequired:
		return FailKindQuotaExhausted
	default:
		return ""
	}
}

// classifyMessage 从错误文案兜底识别分类。
func classifyMessage(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"),
		strings.Contains(lower, "token"), strings.Contains(lower, "登录"), strings.Contains(lower, "鉴权"):
		return FailKindSessionExpired
	case strings.Contains(lower, "rate"), strings.Contains(lower, "too many"), strings.Contains(lower, "频繁"), strings.Contains(lower, "风控"):
		return FailKindRateLimited
	case strings.Contains(lower, "quota"), strings.Contains(lower, "额度"), strings.Contains(lower, "余额"):
		return FailKindQuotaExhausted
	default:
		return ""
	}
}

var sharedHTTPClient = &http.Client{Timeout: 15 * time.Minute}

// NewRelayError 供上层构造中继错误。
func NewRelayError(kind string, format string, args ...any) error {
	return newRelayError(kind, format, args...)
}
