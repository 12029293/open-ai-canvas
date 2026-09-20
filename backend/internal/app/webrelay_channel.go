package app

// 网页中继渠道（DeepSeek 网页版 / 千问网页版）是内置平台渠道：
// 凭据来自账号池而不是渠道表，因此不落 system_channels，
// 任务准入与 provider 配置解析都要为它走专用分支，避免被当成缺失的外部渠道拒绝。

import (
	"context"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/webrelay"
)

const (
	// DeepSeekRelayChannelID DeepSeek 网页版中继渠道。
	DeepSeekRelayChannelID = "deepseek-relay"
	// QwenRelayChannelID 千问网页版中继渠道。
	QwenRelayChannelID = "qwen-relay"

	WebRelayDeepSeekInterface = "webrelay-deepseek"
	WebRelayQwenInterface     = "webrelay-qwen"
)

// IsWebRelayChannel 报告渠道是否为网页中继渠道。
func IsWebRelayChannel(channelID string) bool {
	trimmed := strings.TrimSpace(channelID)
	return trimmed == DeepSeekRelayChannelID || trimmed == QwenRelayChannelID
}

func isWebRelayInterface(interfaceType string) bool {
	trimmed := strings.TrimSpace(interfaceType)
	return trimmed == WebRelayDeepSeekInterface || trimmed == WebRelayQwenInterface
}

// webRelaySiteForInterface 把接口类型映射到中继站点。
func webRelaySiteForInterface(interfaceType string) (string, error) {
	switch strings.TrimSpace(interfaceType) {
	case WebRelayDeepSeekInterface:
		return webrelay.SiteDeepSeek, nil
	case WebRelayQwenInterface:
		return webrelay.SiteQwen, nil
	default:
		return "", fmt.Errorf("接口类型 %s 不是网页中继渠道", interfaceType)
	}
}

// webRelayChannelIDForSite 站点 → 渠道 ID。
func webRelayChannelIDForSite(site string) string {
	if site == webrelay.SiteQwen {
		return QwenRelayChannelID
	}
	return DeepSeekRelayChannelID
}

// webRelaySiteForChannelID 渠道 ID → 站点。
func webRelaySiteForChannelID(channelID string) (string, error) {
	switch strings.TrimSpace(channelID) {
	case DeepSeekRelayChannelID:
		return webrelay.SiteDeepSeek, nil
	case QwenRelayChannelID:
		return webrelay.SiteQwen, nil
	default:
		return "", fmt.Errorf("%s 不是网页中继渠道", channelID)
	}
}

// webRelayModelInfo 渠道模型键定义。
type webRelayModelInfo struct {
	Key         string
	DisplayName string
	Description string
	Thinking    bool
}

// webRelayModels 各站点的文本模型键。
var webRelayModels = map[string][]webRelayModelInfo{
	webrelay.SiteDeepSeek: {
		{Key: "deepseek-chat", DisplayName: "DeepSeek V3（网页中继）", Description: "chat.deepseek.com 网页会话中继，走账号池自动取号。", Thinking: false},
		{Key: "deepseek-reasoner", DisplayName: "DeepSeek R1（网页中继）", Description: "R1 深度思考档，走 chat.deepseek.com 网页会话中继。", Thinking: true},
	},
	webrelay.SiteQwen: {
		{Key: "qwen3.7-plus", DisplayName: "千问 Qwen3.7-Plus（网页中继）", Description: "chat.qwen.ai 网页会话中继，支持文本/图片/视频输入。", Thinking: false},
		{Key: "qwen3.8-max", DisplayName: "千问 Qwen3.8-Max（网页中继）", Description: "Qwen3.8 旗舰，支持文本/图片/视频输入，走网页会话中继。", Thinking: false},
		{Key: "qwen3.8-omni-flash", DisplayName: "千问 Qwen3.8-Omni-Flash（网页中继）", Description: "全模态快版，支持文本/图片/视频/音频输入。", Thinking: false},
	},
}

// webRelayModelCapability 把模型键映射到生成能力；第二返回值表示键是否受支持。
// 允许 <模型>-thinking 变体（-thinking 只表达思考开关，不改变模型）。
func webRelayModelCapability(site string, modelKey string) (string, bool) {
	for _, model := range webRelayModels[site] {
		if model.Key == modelKey {
			return "text", true
		}
	}
	if base := strings.TrimSuffix(modelKey, "-thinking"); base != modelKey {
		for _, model := range webRelayModels[site] {
			if model.Key == base {
				return "text", true
			}
		}
	}
	return "", false
}

// webRelayModelThinking 判断模型键是否为思考档。
func webRelayModelThinking(site string, modelKey string) bool {
	if strings.HasSuffix(modelKey, "-thinking") {
		_, ok := webRelayModelCapability(site, modelKey)
		return ok
	}
	for _, model := range webRelayModels[site] {
		if model.Key == modelKey {
			return model.Thinking
		}
	}
	return false
}

// resolveWebRelayModelSelection 是网页中继渠道的任务准入：无渠道表、无价格档，
// 只校验模型键与任务能力匹配后回填 provider 路由字段。
func (s *Service) resolveWebRelayModelSelection(site string, config map[string]any, taskType string) (map[string]any, error) {
	modelKey := strings.TrimPrefix(strings.TrimSpace(stringValue(config["model"])), "models/")
	if _, ok := webRelayModelCapability(site, modelKey); !ok {
		return nil, InvalidModelSelection("网页中继渠道不支持模型：" + modelKey)
	}
	if taskType != "" && taskType != "text" && taskType != "canvas_text" {
		return nil, ModelCapabilityNotSupported("网页中继渠道仅支持文本任务")
	}
	nextConfig := make(map[string]any, len(config)+2)
	for key, value := range config {
		switch key {
		case "channelId", "channelModelKey", "priceTierId", "providerModelKey", "apiFormat", "interfaceType", "baseUrl", "apiKey", "secretKey", "headers", "model", "capabilityConfig":
			continue
		default:
			nextConfig[key] = value
		}
	}
	nextConfig["channelId"] = webRelayChannelIDForSite(site)
	nextConfig["model"] = modelKey
	nextConfig["channelModelKey"] = modelKey
	nextConfig["providerModelKey"] = modelKey
	nextConfig["interfaceType"] = webRelayInterfaceForSite(site)
	nextConfig["apiFormat"] = "openai"
	nextConfig["priceTierId"] = ""
	return nextConfig, nil
}

func webRelayInterfaceForSite(site string) string {
	if site == webrelay.SiteQwen {
		return WebRelayQwenInterface
	}
	return WebRelayDeepSeekInterface
}

// webRelayDirectCredentials 解析个人渠道随任务携带的网页凭据（按行分隔）。
// 千问支持整段 Cookie：保留全部风控 cookie 并提取 token；DeepSeek 是裸 userToken。
func webRelayDirectCredentials(site string, raw string) []webrelay.Credential {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	lines := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' })
	credentials := make([]webrelay.Credential, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, ",;")
		if line == "" {
			continue
		}
		token := webrelay.NormalizeToken(site, line)
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		credentials = append(credentials, webrelay.Credential{
			AccountID: fmt.Sprintf("personal-%d", len(credentials)+1),
			Site:      site,
			Token:     token,
			Cookie:    webrelay.NormalizeCookieHeader(site, line, token),
		})
	}
	return credentials
}

// runWebRelayTextTask 网页中继文本任务：个人渠道优先用任务携带的 token 直连，
// 否则走内置账号池取号；失败自动换凭据重试，流式输出只在首个增量前允许重试。
func (s *Service) runWebRelayTextTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	site, err := webRelaySiteForInterface(input.Config.InterfaceType)
	if err != nil {
		return nil, err
	}
	attachments, err := webRelayAttachments(site, input)
	if err != nil {
		return nil, err
	}
	modelKey := strings.TrimPrefix(strings.TrimSpace(input.Config.Model), "models/")
	credentials := webRelayDirectCredentials(site, input.Config.APIKey)
	if len(credentials) == 0 {
		// 内置平台渠道模式：凭据来自账号池，只允许预置模型键。
		if _, ok := webRelayModelCapability(site, modelKey); !ok {
			return nil, InvalidModelSelection("网页中继渠道不支持模型：" + modelKey)
		}
	} else if strings.TrimSpace(modelKey) == "" {
		return nil, InvalidModelSelection("请先选择要使用的模型")
	}
	thinking := input.TextOptions.Thinking || webRelayModelThinking(site, modelKey)

	history := make([]webrelay.ChatMessage, 0, len(input.TextHistory))
	for _, message := range input.TextHistory {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if (role != "user" && role != "assistant") || content == "" {
			continue
		}
		history = append(history, webrelay.ChatMessage{Role: role, Content: content})
	}
	request := webrelay.ChatRequest{
		Model:        modelKey,
		SystemPrompt: strings.TrimSpace(input.Config.SystemPrompt),
		History:      history,
		Prompt:       input.Prompt,
		Thinking:     thinking,
		Attachments:  attachments,
	}

	// 流式输出只允许在首个增量到来前失败重试，避免重复推送正文。
	emitted := false
	handlers := webrelay.StreamHandlers{
		OnText: func(delta string) {
			emitted = true
			if input.OnTextDelta != nil {
				input.OnTextDelta(delta)
			}
		},
		OnReasoning: func(delta string) {
			if input.OnReasoningDelta != nil {
				input.OnReasoningDelta(delta)
			}
		},
	}

	// 千问走 CDP 浏览器 UI 中继（阿里云 WAF 对 API 反爬，凭据直连必被 RGV587 拦截）。
	if site == webrelay.SiteQwen {
		result, generateErr := s.qwenChatViaCDP(ctx, request, handlers)
		if generateErr != nil {
			return nil, generateErr
		}
		payload := map[string]interface{}{"mode": "text", "text": result.Text}
		if strings.TrimSpace(result.Reasoning) != "" {
			payload["reasoning"] = result.Reasoning
		}
		return payload, nil
	}

	if len(credentials) > 0 {
		return runWebRelayWithCredentials(ctx, credentials, request, handlers, &emitted)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		credential, pickErr := s.WebRelayPick(site)
		if pickErr != nil {
			if lastErr != nil {
				return nil, fmt.Errorf("%v；且后续取号失败：%w", lastErr, pickErr)
			}
			return nil, pickErr
		}
		result, generateErr := webrelay.GenerateChat(ctx, *credential, request, handlers)
		if generateErr == nil {
			_ = s.WebRelayMarkSuccess(credential.AccountID)
			payload := map[string]interface{}{"mode": "text", "text": result.Text}
			if strings.TrimSpace(result.Reasoning) != "" {
				payload["reasoning"] = result.Reasoning
			}
			return payload, nil
		}
		kind := webrelay.ErrorKind(generateErr)
		_ = s.WebRelayMarkFailed(credential.AccountID, webrelay.MarkFailedOptions{Kind: kind, Message: generateErr.Error()})
		lastErr = fmt.Errorf("账号 %s 生成失败：%w", credential.AccountID, generateErr)
		if emitted {
			return nil, lastErr
		}
		switch kind {
		case webrelay.FailKindSessionExpired, webrelay.FailKindRateLimited, webrelay.FailKindQuotaExhausted:
			continue
		default:
			return nil, lastErr
		}
	}
	return nil, lastErr
}

// runWebRelayWithCredentials 个人渠道直连：逐个凭据尝试；登录失效/限流跳到下一个。
func runWebRelayWithCredentials(ctx context.Context, credentials []webrelay.Credential, request webrelay.ChatRequest, handlers webrelay.StreamHandlers, emitted *bool) (map[string]interface{}, error) {
	var lastErr error
	for _, credential := range credentials {
		result, generateErr := webrelay.GenerateChat(ctx, credential, request, handlers)
		if generateErr == nil {
			payload := map[string]interface{}{"mode": "text", "text": result.Text}
			if strings.TrimSpace(result.Reasoning) != "" {
				payload["reasoning"] = result.Reasoning
			}
			return payload, nil
		}
		lastErr = fmt.Errorf("凭据 %s 生成失败：%w", credential.AccountID, generateErr)
		if *emitted {
			return nil, lastErr
		}
		switch webrelay.ErrorKind(generateErr) {
		case webrelay.FailKindSessionExpired, webrelay.FailKindRateLimited, webrelay.FailKindQuotaExhausted:
			continue
		default:
			return nil, lastErr
		}
	}
	return nil, lastErr
}

// webRelayAttachments 收集文本任务的图片/视频参考素材。千问网页版支持多模态输入，
// 附件会先上传到千问 OSS 再随消息引用；DeepSeek 网页版是纯文本，带素材时明确报错。
func webRelayAttachments(site string, input canvasGenerationInput) ([]webrelay.ChatAttachment, error) {
	total := len(input.ReferenceImages) + len(input.ReferenceVideos)
	if total == 0 {
		return nil, nil
	}
	if site != webrelay.SiteQwen {
		return nil, fmt.Errorf("DeepSeek 网页版是纯文本模型，不支持图片/视频输入；请改用千问网页渠道或支持视觉的开放协议")
	}
	attachments := make([]webrelay.ChatAttachment, 0, total)
	appendMedia := func(medias []providerMedia, kind string) error {
		for _, media := range medias {
			raw, mimeType, err := mediaBytes(media)
			if err != nil {
				return fmt.Errorf("读取%s参考素材失败：%w", kind, err)
			}
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			name := strings.TrimSpace(media.Name)
			if name == "" {
				name = fmt.Sprintf("%s-reference-%d%s", kind, len(attachments)+1, mediaExtension(mimeType))
			}
			attachments = append(attachments, webrelay.ChatAttachment{Name: name, MimeType: mimeType, Data: raw})
		}
		return nil
	}
	if err := appendMedia(input.ReferenceImages, "image"); err != nil {
		return nil, err
	}
	if err := appendMedia(input.ReferenceVideos, "video"); err != nil {
		return nil, err
	}
	return attachments, nil
}

// mediaExtension 根据 MIME 类型推断附件文件名后缀。
func mediaExtension(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/png"):
		return ".png"
	case strings.HasPrefix(mimeType, "image/jpeg"):
		return ".jpg"
	case strings.HasPrefix(mimeType, "image/webp"):
		return ".webp"
	case strings.HasPrefix(mimeType, "image/gif"):
		return ".gif"
	case strings.HasPrefix(mimeType, "video/mp4"):
		return ".mp4"
	case strings.HasPrefix(mimeType, "video/webm"):
		return ".webm"
	case strings.HasPrefix(mimeType, "video/quicktime"):
		return ".mov"
	default:
		return ""
	}
}
