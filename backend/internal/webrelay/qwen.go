package webrelay

// 千问网页版（chat.qwen.ai）中继客户端。
//
// 请求形态（与网页端一致，参考 qwen-api 等实现）：
//  POST https://chat.qwen.ai/api/chat/completions
//  Headers: Authorization: Bearer <token>、Cookie（可选整段）
//  Body: {model, messages:[{role, content, chat_type:"t2t", feature_config:{thinking_enabled, output_schema}}], stream:true, incremental_output:true}
//  SSE 帧：data: {"choices":[{"delta":{"content","phase","status"}}], ...}，phase=thinking 时为思考增量。

import (
	"infinite-canvas/backend/internal/outbound"
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	qwenBaseURL   = "https://chat.qwen.ai"
	qwenUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

func qwenHeaders(credential Credential, cookie string) map[string]string {
	headers := map[string]string{
		"User-Agent":        qwenUserAgent,
		"Content-Type":      "application/json",
		"Accept":            "text/event-stream",
		"Authorization":     "Bearer " + credential.Token,
		"X-FE-Version":      "prod-fe-1.0",
		"X-Accel-Buffering": "no",
		"Referer":           qwenBaseURL + "/",
		"Origin":            qwenBaseURL,
		"x-request-id":      uuid.NewString(),
	}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	return headers
}

// qwenBootstrapCookie 访问主页预热拿 WAF cookie（acw_tc 等），与凭据自带 Cookie 合并。
func qwenBootstrapCookie(ctx context.Context, credential Credential) string {
	merged := credential.Cookie
	warmReq, err := http.NewRequestWithContext(ctx, http.MethodGet, qwenBaseURL+"/", nil)
	if err != nil {
		return merged
	}
	warmReq.Header.Set("User-Agent", qwenUserAgent)
	warmReq.Header.Set("Accept", "text/html")
	resp, err := relayHTTPClient(ctx).Do(warmReq)
	if err != nil {
		return merged
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "" || cookie.Value == "" {
			continue
		}
		if strings.Contains(merged, cookie.Name+"=") {
			continue
		}
		if merged == "" {
			merged = cookie.Name + "=" + cookie.Value
		} else {
			merged += "; " + cookie.Name + "=" + cookie.Value
		}
	}
	return merged
}

// qwenWebModel 把渠道模型键翻译成网页模型名（-thinking 后缀只表达思考开关）。
func qwenWebModel(modelKey string) string {
	return strings.TrimSuffix(strings.TrimSpace(modelKey), "-thinking")
}

// QwenChat 执行一次千问网页版对话。
func QwenChat(ctx context.Context, credential Credential, req ChatRequest, handlers StreamHandlers) (ChatResult, error) {
	messages := make([]map[string]any, 0, len(req.History)+2)
	if system := strings.TrimSpace(req.SystemPrompt); system != "" {
		messages = append(messages, map[string]any{
			"role":         "system",
			"content":      system,
			"chat_type":    "t2t",
			"feature_config": map[string]any{"thinking_enabled": false, "output_schema": nil},
		})
	}
	for _, message := range req.History {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content := strings.TrimSpace(message.Content)
		if (role != "user" && role != "assistant") || content == "" {
			continue
		}
		messages = append(messages, map[string]any{
			"role":         role,
			"content":      content,
			"chat_type":    "t2t",
			"feature_config": map[string]any{"thinking_enabled": false, "output_schema": nil},
		})
	}
	messages = append(messages, map[string]any{
		"role":         "user",
		"content":      req.Prompt,
		"chat_type":    "t2t",
		"feature_config": map[string]any{"thinking_enabled": req.Thinking, "output_schema": "phase"},
	})
	// 附件（图片/视频）：先上传到千问 OSS，再把文件元数据挂到最后一条用户消息上。
	if len(req.Attachments) > 0 {
		files := make([]map[string]any, 0, len(req.Attachments))
		for _, attachment := range req.Attachments {
			fileInfo, uploadErr := qwenUploadAttachment(ctx, credential, attachment)
			if uploadErr != nil {
				return ChatResult{}, uploadErr
			}
			files = append(files, fileInfo)
		}
		userMessage := messages[len(messages)-1]
		userMessage["files"] = files
		userMessage["fid"] = uuid.NewString()
		userMessage["childrenIds"] = []string{uuid.NewString()}
		userMessage["user_action"] = "chat"
		userMessage["timestamp"] = time.Now().UnixMilli()
		userMessage["sub_chat_type"] = "t2t"
		userMessage["extra"] = map[string]any{"meta": map[string]any{"subChatType": "t2t"}}
	}
	body := map[string]any{
		"model":              qwenWebModel(req.Model),
		"messages":           messages,
		"stream":             true,
		"incremental_output": true,
		"chat_id":            uuid.NewString(),
		"session_id":         uuid.NewString(),
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ChatResult{}, err
	}

	cookie := qwenBootstrapCookie(ctx, credential)
	resp, endpoint, err := qwenPost(ctx, credential, cookie, "/api/v2/chat/completions", encoded)
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet := ""
		if data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096)); readErr == nil {
			snippet = string(data)
		}
		// v1 老端点在网关层挂死（恒 504）：主用 v2，404/405/504 时回退 v1 重试一次。
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusGatewayTimeout {
			if endpoint == "/api/v2/chat/completions" {
				resp2, _, err2 := qwenPost(ctx, credential, cookie, "/api/chat/completions", encoded)
				if err2 == nil {
					defer resp2.Body.Close()
					if resp2.StatusCode == http.StatusOK {
						return consumeQwenStream(resp2.Body, handlers)
					}
					snippet2 := ""
					if data, readErr := io.ReadAll(io.LimitReader(resp2.Body, 4096)); readErr == nil {
						snippet2 = string(data)
					}
					resp, snippet = resp2, snippet2
				}
			}
		}
		kind := classifyHTTPStatus(resp.StatusCode)
		if kind == "" {
			kind = classifyMessage(snippet)
		}
		return ChatResult{}, newRelayError(kind, "千问对话接口返回 %d：%s", resp.StatusCode, truncate(snippet, 300))
	}
	return consumeQwenStream(resp.Body, handlers)
}

func qwenPost(ctx context.Context, credential Credential, cookie string, endpoint string, body []byte) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qwenBaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, endpoint, err
	}
	for key, value := range qwenHeaders(credential, cookie) {
		req.Header.Set(key, value)
	}
	resp, err := relayHTTPClient(ctx).Do(req)
	if err != nil {
		return nil, endpoint, newRelayError("", "千问请求失败：%v", err)
	}
	return resp, endpoint, nil
}

// consumeQwenStream 解析千问 SSE 增量流。
func consumeQwenStream(reader io.Reader, handlers StreamHandlers) (ChatResult, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	var result ChatResult
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, ":") {
			continue
		}
		raw := strings.TrimPrefix(line, "data:")
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "[DONE]" {
			continue
		}
		if strings.Contains(raw, "RGV587") || strings.Contains(raw, "FAIL_SYS_USER_VALIDATE") || strings.Contains(raw, "aliyun_waf_aa") {
			return result, newRelayError(FailKindRateLimited, "千问风控拦截（RGV587）：请在渠道设置里改用整段 Cookie 凭据——F12 → 网络 → 任意请求 → 复制 Cookie 请求头的完整值")
		}
		var payload struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Phase            string `json:"phase"`
					Status           string `json:"status"`
				} `json:"delta"`
			} `json:"choices"`
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			continue
		}
		if payload.Code != 0 && payload.Message != "" {
			kind := classifyMessage(payload.Message)
			return result, newRelayError(kind, "千问流式错误：%s", payload.Message)
		}
		// 嵌套错误结构（如 {"error":{"code":"invalid_input","details":"输入或附件无效。…"}}）
		// 不带顶层 code/message，之前会被静默跳过、最终误报成「可能被风控或额度耗尽」。
		var nested struct {
			Error *struct {
				Code    string `json:"code"`
				Details string `json:"details"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &nested); err == nil && nested.Error != nil {
			reason := firstNonEmpty(nested.Error.Details, nested.Error.Message, nested.Error.Code)
			if reason != "" {
				return result, newRelayError(FailKindInvalidInput, "千问拒绝本次输入（%s）：%s", nested.Error.Code, reason)
			}
		}
		for _, choice := range payload.Choices {
			chunk := choice.Delta.Content
			if chunk == "" {
				continue
			}
			if strings.EqualFold(choice.Delta.Phase, "thinking") || choice.Delta.ReasoningContent != "" {
				thinking := firstNonEmpty(choice.Delta.ReasoningContent, chunk)
				result.Reasoning += thinking
				if handlers.OnReasoning != nil {
					handlers.OnReasoning(thinking)
				}
				continue
			}
			result.Text += chunk
			if handlers.OnText != nil {
				handlers.OnText(chunk)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, newRelayError("", "千问流式响应读取失败：%v", err)
	}
	if strings.TrimSpace(result.Text) == "" && strings.TrimSpace(result.Reasoning) == "" {
		return result, newRelayError(FailKindRateLimited, "千问没有返回内容（可能被风控或额度耗尽）")
	}
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// GenerateChat 站点分发的统一入口。
// relayHTTPClient 遵循账号绑定的代理（ctx 注入）；未绑定时与 sharedHTTPClient 行为一致。
func relayHTTPClient(ctx context.Context) *http.Client {
	return outbound.HTTPClientFromContext(ctx, 15*time.Minute)
}

// GenerateChat 站点分发的统一入口。
func GenerateChat(ctx context.Context, credential Credential, req ChatRequest, handlers StreamHandlers) (ChatResult, error) {
	if credential.ProxyURL != "" {
		ctx = outbound.WithProxyURL(ctx, credential.ProxyURL)
	}
	switch credential.Site {
	case SiteDeepSeek:
		return DeepSeekChat(ctx, credential, req, handlers)
	case SiteQwen:
		return QwenChat(ctx, credential, req, handlers)
	default:
		return ChatResult{}, fmt.Errorf("不支持的中继站点：%s", credential.Site)
	}
}

// qwenFiletype 把 MIME 类型映射到千问上传接口的 filetype（image/video/file）。
func qwenFiletype(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	default:
		return "file"
	}
}

// qwenUploadAttachment 把附件上传到千问网页版 OSS：
// 1) POST /api/v2/files/getstsToken 拿 STS 临时凭据；
// 2) 以 POST 表单（V1 签名）直传阿里云 OSS；
// 返回对话请求 files 数组需要的文件元数据。
func qwenUploadAttachment(ctx context.Context, credential Credential, attachment ChatAttachment) (map[string]any, error) {
	if len(attachment.Data) == 0 {
		return nil, newRelayError("", "千问附件内容为空")
	}
	mimeType := strings.TrimSpace(attachment.MimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	filename := strings.TrimSpace(attachment.Name)
	if filename == "" {
		filename = "attachment-" + uuid.NewString()
	}
	filetype := qwenFiletype(mimeType)

	stsBody, err := json.Marshal(map[string]any{
		"filename": filename,
		"filesize": len(attachment.Data),
		"filetype": filetype,
	})
	if err != nil {
		return nil, err
	}
	resp, _, err := qwenPost(ctx, credential, qwenBootstrapCookie(ctx, credential), "/api/v2/files/getstsToken", stsBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet := ""
		if data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2048)); readErr == nil {
			snippet = string(data)
		}
		return nil, newRelayError(classifyHTTPStatus(resp.StatusCode), "千问上传授权接口返回 %d：%s", resp.StatusCode, truncate(snippet, 200))
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, newRelayError("", "读取千问上传授权响应失败：%v", err)
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	sts := map[string]any{}
	if json.Unmarshal(payload, &envelope) == nil && envelope.Data != nil {
		sts = envelope.Data
	} else {
		var flat map[string]any
		if json.Unmarshal(payload, &flat) == nil {
			if nested, ok := flat["data"].(map[string]any); ok {
				sts = nested
			} else {
				sts = flat
			}
		}
	}
	need := []string{"access_key_id", "access_key_secret", "security_token", "bucketname", "file_path", "file_url"}
	for _, key := range need {
		if value := stringValue(sts[key]); value == "" {
			return nil, newRelayError("", "千问上传授权响应缺少 %s", key)
		}
	}
	accessKeyID := stringValue(sts["access_key_id"])
	accessKeySecret := stringValue(sts["access_key_secret"])
	securityToken := stringValue(sts["security_token"])
	bucket := stringValue(sts["bucketname"])
	objectKey := stringValue(sts["file_path"])
	fileURL := stringValue(sts["file_url"])
	endpoint := stringValue(sts["endpoint"])
	if endpoint == "" {
		if region := stringValue(sts["region"]); region != "" {
			endpoint = region + ".aliyuncs.com"
		} else {
			endpoint = "oss-accelerate.aliyuncs.com"
		}
	}

	// OSS PostObject：policy base64 + HMAC-SHA1 签名。
	policyDocument := map[string]any{
		"expiration": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		"conditions": []any{
			map[string]any{"bucket": bucket},
			[]any{"content-length-range", 1, len(attachment.Data)},
		},
	}
	policyRaw, err := json.Marshal(policyDocument)
	if err != nil {
		return nil, err
	}
	policyEncoded := base64.StdEncoding.EncodeToString(policyRaw)
	mac := hmac.New(sha1.New, []byte(accessKeySecret))
	mac.Write([]byte(policyEncoded))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	formBuffer := &bytes.Buffer{}
	writer := multipart.NewWriter(formBuffer)
	for _, field := range []struct{ key, value string }{
		{"key", objectKey},
		{"policy", policyEncoded},
		{"OSSAccessKeyId", accessKeyID},
		{"signature", signature},
		{"x-oss-security-token", securityToken},
		{"Content-Type", mimeType},
		{"success_action_status", "200"},
	} {
		if err := writer.WriteField(field.key, field.value); err != nil {
			return nil, err
		}
	}
	fileHeader, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := fileHeader.Write(attachment.Data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+bucket+"."+endpoint+"/", bytes.NewReader(formBuffer.Bytes()))
	if err != nil {
		return nil, err
	}
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	uploadResp, err := relayHTTPClient(ctx).Do(uploadReq)
	if err != nil {
		return nil, newRelayError("", "千问附件上传失败：%v", err)
	}
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode >= 300 {
		snippet := ""
		if data, readErr := io.ReadAll(io.LimitReader(uploadResp.Body, 2048)); readErr == nil {
			snippet = string(data)
		}
		return nil, newRelayError("", "千问附件上传被拒绝（HTTP %d）：%s", uploadResp.StatusCode, truncate(snippet, 200))
	}

	fileID := stringValue(sts["file_id"])
	if fileID == "" {
		fileID = uuid.NewString()
	}
	now := time.Now().UnixMilli()
	showType := filetype
	fileClass := "vision"
	if filetype == "video" {
		fileClass = "video"
	}
	return map[string]any{
		"type": filetype,
		"file": map[string]any{
			"created_at": now,
			"data":       map[string]any{},
			"filename":   filename,
			"hash":       nil,
			"id":         fileID,
			"user_id":    "",
			"meta":       map[string]any{"name": filename, "size": len(attachment.Data), "content_type": mimeType},
			"update_at":  now,
		},
		"id":              fileID,
		"url":             fileURL,
		"name":            filename,
		"collection_name": "",
		"progress":        0,
		"status":          "uploaded",
		"greenNet":        "success",
		"size":            len(attachment.Data),
		"error":           "",
		"itemId":          uuid.NewString(),
		"file_type":       mimeType,
		"showType":        showType,
		"file_class":      fileClass,
		"uploadTaskId":    uuid.NewString(),
	}, nil
}

// stringValue 读取 STS/上传响应中的字符串字段。
func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

// ConsumeQwenStream 供上层解析千问 SSE 文本。
func ConsumeQwenStream(reader io.Reader, handlers StreamHandlers) (ChatResult, error) {
	return consumeQwenStream(reader, handlers)
}
