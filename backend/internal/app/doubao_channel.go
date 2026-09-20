package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// 豆包账号池是内置平台渠道：凭据来自账号池而不是渠道表，因此不落 system_channels，
// 任务准入与 provider 配置解析都要为它走专用分支，避免被当成缺失的外部渠道拒绝。
const (
	DoubaoPoolChannelID     = "doubao-pool"
	DoubaoPoolInterfaceType = "doubao-pool"
)

const doubaoPoolImageModel = "doubao-seedream-image"

// IsDoubaoPoolChannel reports whether the task targets the built-in account-pool channel.
func IsDoubaoPoolChannel(channelID string) bool {
	return strings.TrimSpace(channelID) == DoubaoPoolChannelID
}

func isDoubaoPoolInterface(interfaceType string) bool {
	return strings.TrimSpace(interfaceType) == DoubaoPoolInterfaceType
}

// doubaoPoolModelCapability 把账号池模型键映射到生成能力；第二返回值表示键是否受支持。
// doubao-seedance-video 是历史默认键，继续按 Mini 语义接受。
func doubaoPoolModelCapability(modelKey string) (string, bool) {
	key := strings.TrimSpace(modelKey)
	if key == doubaoPoolImageModel {
		return "image", true
	}
	if key == "doubao-seedance-video" || strings.HasPrefix(key, "doubao-seedance-video-") {
		return "video", true
	}
	return "", false
}

// doubaoPoolVideoVariant 把前端模型键翻译成豆包会话里的模型描述。
// 与参考实现 mapAbilityModel 对齐：fast/mini 都落到 seedance_v2.0_fast 档，
// 未带后缀的历史键按 Mini 处理。
func doubaoPoolVideoVariant(modelKey string) string {
	key := strings.ToLower(strings.TrimSpace(modelKey))
	if strings.Contains(key, "fast") {
		return "Seedance 2.0 Fast"
	}
	return "Seedance 2.0 Mini"
}

func doubaoPoolCapabilityForMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "video":
		return "video"
	case "image":
		return "image"
	default:
		return ""
	}
}

// runDoubaoPoolVideoTask 文生视频：池内取号生成，产物下载后以 dataUrl 回填任务结果，
// 与其他视频协议的任务结果形状保持一致。
func (s *Service) runDoubaoPoolVideoTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	duration := 0
	if seconds := strings.TrimSpace(input.Config.VideoSeconds); seconds != "" {
		if parsed, err := parsePositiveInt(seconds); err == nil {
			duration = parsed
		}
	}
	result, err := s.DoubaoGenerateVideo(ctx, DoubaoGenerateVideoRequest{
		Prompt:   input.Prompt,
		Model:    doubaoPoolVideoVariant(input.Config.Model),
		Duration: duration,
		Ratio:    strings.TrimSpace(input.Config.Size),
	})
	if err != nil {
		return nil, err
	}
	data, mimeType, err := s.downloadDoubaoPoolMedia(ctx, input.Config, result.URLs, "视频")
	if err != nil {
		return nil, err
	}
	// 无水印保证在生成层完成（doubao/fallback.go 母片通道，实测是真无水印原片）。
	// 不做 delogo 遮盖：用户要求高清原片，宁要原样水印也不要打码画质；
	// 母片通道失败时 URLs[0] 仍是干净度最高的候选（preferWatermarkFree 已滤过）。
	return map[string]interface{}{"mode": "video", "video": map[string]interface{}{"dataUrl": dataURL(mimeType, data), "mimeType": mimeType}}, nil
}

// runDoubaoPoolImageTask 文生图：池内取号生成，全部产物转 dataUrl。
func (s *Service) runDoubaoPoolImageTask(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	result, err := s.DoubaoGenerateImage(ctx, DoubaoGenerateImageRequest{
		Prompt: input.Prompt,
		Ratio:  strings.TrimSpace(input.Config.Size),
	})
	if err != nil {
		return nil, err
	}
	images := make([]map[string]string, 0, len(result.URLs))
	for _, rawURL := range result.URLs {
		data, mimeType, err := s.downloadDoubaoPoolMedia(ctx, input.Config, []string{rawURL}, "图片")
		if err != nil {
			return nil, err
		}
		images = append(images, map[string]string{"dataUrl": dataURL(mimeType, data), "mimeType": mimeType})
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("豆包账号池未返回图片：%s", strings.TrimSpace(result.Text))
	}
	return map[string]interface{}{"mode": "image", "images": images}, nil
}

func (s *Service) downloadDoubaoPoolMedia(ctx context.Context, config providerConfig, urls []string, label string) ([]byte, string, error) {
	var lastErr error
	for _, rawURL := range urls {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" {
			continue
		}
		downloadCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		data, mimeType, err := getProviderExternalBinary(withProviderRequestKind(downloadCtx, "download"), config, trimmed)
		cancel()
		if err == nil && len(data) > 0 {
			return data, normalizedMediaMimeType(mimeType, data), nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, "", fmt.Errorf("豆包%s下载失败：%w", label, lastErr)
	}
	return nil, "", fmt.Errorf("豆包账号池未返回%s地址", label)
}

func parsePositiveInt(value string) (int, error) {
	var parsed int
	_, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("无效的正整数：%s", value)
	}
	return parsed, nil
}

// resolveDoubaoPoolModelSelection 是账号池渠道的任务准入：无渠道表、无价格档，
// 只校验模型键与任务能力匹配后回填 provider 路由字段。
func (s *Service) resolveDoubaoPoolModelSelection(config map[string]any, taskType string) (map[string]any, error) {
	modelKey := strings.TrimPrefix(strings.TrimSpace(stringValue(config["model"])), "models/")
	capability, ok := doubaoPoolModelCapability(modelKey)
	if !ok {
		return nil, InvalidModelSelection("豆包账号池不支持模型：" + modelKey)
	}
	if want := doubaoPoolCapabilityForMode(taskType); want != "" && want != capability {
		return nil, ModelCapabilityNotSupported("所选模型与任务能力不匹配")
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
	nextConfig["channelId"] = DoubaoPoolChannelID
	nextConfig["model"] = modelKey
	nextConfig["channelModelKey"] = modelKey
	nextConfig["providerModelKey"] = modelKey
	nextConfig["interfaceType"] = DoubaoPoolInterfaceType
	nextConfig["apiFormat"] = "openai"
	nextConfig["priceTierId"] = ""
	return nextConfig, nil
}
