package webrelay

// DeepSeek 网页版（chat.deepseek.com）中继客户端。
//
// 请求形态（与网页/移动端一致）：
//  1. POST /api/v0/chat_session/create  {"agent":"chat"} → 会话 ID
//  2. POST /api/v0/chat/create_pow_challenge {"target_path":"/api/v0/chat/completion"} → PoW 挑战
//  3. POST /api/v0/chat/completion（携带 X-Ds-Pow-Response）→ SSE 流
//     SSE 帧：data: {"v": <增量>, "p": "response/thinking_content" | "response/content" | ...}
//
// 历史对话：网页接口单次只接受一条 prompt，多轮上下文以纯文本形式拼进 prompt。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const deepSeekBaseURL = "https://chat.deepseek.com/api/v0"

// deepSeekClientVersion 模拟的安卓客户端版本。
// 2026-09 实测：服务端最低版本门槛已提升，1.3.3 会被拒（40005 CLIENT_VERSION_TOO_LOW），
// 2.0.0 起版本检查通过。若日后再次报错，先升这个版本号对照探测。
const deepSeekClientVersion = "2.2.6"

func deepSeekHeaders(token string, bodyLen int) map[string]string {
	headers := map[string]string{
		"User-Agent":        "DeepSeek/" + deepSeekClientVersion + " Android/34",
		"Content-Type":      "application/json",
		"Accept":            "application/json",
		"Accept-Charset":    "UTF-8",
		"X-Client-Locale":   "zh_CN",
		"X-Client-Version":  deepSeekClientVersion,
		"X-Client-Platform": "android",
	}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if bodyLen > 0 {
		headers["Content-Length"] = fmt.Sprintf("%d", bodyLen)
	}
	return headers
}

// deepSeekJSON 发起普通 JSON 请求并解析响应。
func deepSeekJSON(ctx context.Context, token string, path string, body any, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepSeekBaseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	for key, value := range deepSeekHeaders(token, len(encoded)) {
		req.Header.Set(key, value)
	}
	resp, err := relayHTTPClient(ctx).Do(req)
	if err != nil {
		return newRelayError("", "DeepSeek 请求失败：%v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return newRelayError("", "DeepSeek 响应读取失败：%v", err)
	}
	if resp.StatusCode != http.StatusOK {
		kind := classifyHTTPStatus(resp.StatusCode)
		return newRelayError(kind, "DeepSeek 接口返回 %d：%s", resp.StatusCode, truncate(string(data), 300))
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Code   int             `json:"code"`
		Msg    string          `json:"msg"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return newRelayError("", "DeepSeek 响应解析失败：%v", err)
	}
	if envelope.Code != 0 && envelope.Code != http.StatusOK && envelope.Msg != "" {
		kind := classifyMessage(envelope.Msg)
		return newRelayError(kind, "DeepSeek 接口错误：%s", envelope.Msg)
	}
	payload := envelope.Data
	if len(payload) == 0 {
		payload = data
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return newRelayError("", "DeepSeek 响应字段解析失败：%v", err)
	}
	return nil
}

// deepSeekCreateSession 创建会话，返回会话 ID。
func deepSeekCreateSession(ctx context.Context, token string) (string, error) {
	var payload struct {
		BizData json.RawMessage `json:"biz_data"`
		ID      string          `json:"id"`
	}
	if err := deepSeekJSON(ctx, token, "/chat_session/create", map[string]any{"agent": "chat"}, &payload); err != nil {
		return "", err
	}
	if payload.ID != "" {
		return payload.ID, nil
	}
	var biz struct {
		ID            string `json:"id"`
		ChatSessionID string `json:"chat_session_id"`
		ChatSession   struct {
			ID string `json:"id"`
		} `json:"chat_session"`
	}
	if err := json.Unmarshal(payload.BizData, &biz); err != nil {
		return "", newRelayError("", "DeepSeek 会话创建失败：响应缺少会话 ID")
	}
	if biz.ChatSessionID != "" {
		return biz.ChatSessionID, nil
	}
	if biz.ChatSession.ID != "" {
		return biz.ChatSession.ID, nil
	}
	if biz.ID != "" {
		return biz.ID, nil
	}
	return "", newRelayError("", "DeepSeek 会话创建失败：响应缺少会话 ID")
}

// deepSeekDeleteSession 删除会话（尽力而为）。
func deepSeekDeleteSession(ctx context.Context, token string, sessionID string) {
	_ = deepSeekJSON(ctx, token, "/chat_session/delete", map[string]any{"chat_session_id": sessionID}, &struct{}{})
}

// deepSeekPowHeader 获取挑战并求解 x-ds-pow-response。
// 兼容两种响应形态：旧版挑战字段直接在 data 下；新版包在 data.biz_data.challenge 里。
func deepSeekPowHeader(ctx context.Context, token string) (string, error) {
	var payload struct {
		Algorithm  string `json:"algorithm"`
		Challenge  string `json:"challenge"`
		Salt       string `json:"salt"`
		Difficulty int    `json:"difficulty"`
		ExpireAt   int64  `json:"expire_at"`
		Signature  string `json:"signature"`
		TargetPath string `json:"target_path"`
		BizData    struct {
			Challenge struct {
				Algorithm  string `json:"algorithm"`
				Challenge  string `json:"challenge"`
				Salt       string `json:"salt"`
				Difficulty int    `json:"difficulty"`
				ExpireAt   int64  `json:"expire_at"`
				Signature  string `json:"signature"`
				TargetPath string `json:"target_path"`
			} `json:"challenge"`
		} `json:"biz_data"`
	}
	if err := deepSeekJSON(ctx, token, "/chat/create_pow_challenge", map[string]any{"target_path": "/api/v0/chat/completion"}, &payload); err != nil {
		return "", err
	}
	c := payload.BizData.Challenge
	if c.Challenge == "" {
		c = struct {
			Algorithm  string `json:"algorithm"`
			Challenge  string `json:"challenge"`
			Salt       string `json:"salt"`
			Difficulty int    `json:"difficulty"`
			ExpireAt   int64  `json:"expire_at"`
			Signature  string `json:"signature"`
			TargetPath string `json:"target_path"`
		}{payload.Algorithm, payload.Challenge, payload.Salt, payload.Difficulty, payload.ExpireAt, payload.Signature, payload.TargetPath}
	}
	if c.Challenge == "" {
		return "", newRelayError("", "DeepSeek PoW 挑战获取失败：响应缺少挑战字段")
	}
	header, err := solvePowChallenge(c.Algorithm, c.Challenge, c.Salt, c.Difficulty, c.ExpireAt, c.Signature, c.TargetPath)
	if err != nil {
		return "", newRelayError(FailKindRateLimited, "DeepSeek PoW 求解失败：%v", err)
	}
	return header, nil
}

// deepSeekFlattenPrompt 把多轮上下文拼进单条 prompt。
func deepSeekFlattenPrompt(req ChatRequest) string {
	var builder strings.Builder
	if system := strings.TrimSpace(req.SystemPrompt); system != "" {
		builder.WriteString("【系统设定】\n")
		builder.WriteString(system)
		builder.WriteString("\n\n")
	}
	if len(req.History) > 0 {
		builder.WriteString("【之前的对话记录（供参考，按时间先后）】\n")
		for _, message := range req.History {
			role := "用户"
			if strings.EqualFold(message.Role, "assistant") {
				role = "助手"
			}
			builder.WriteString(role)
			builder.WriteString("：")
			builder.WriteString(strings.TrimSpace(message.Content))
			builder.WriteString("\n")
		}
		builder.WriteString("\n【结束】请基于以上上下文回答用户的最新消息。\n\n")
	}
	builder.WriteString(req.Prompt)
	return builder.String()
}

// DeepSeekChat 执行一次 DeepSeek 网页版对话。
func DeepSeekChat(ctx context.Context, credential Credential, req ChatRequest, handlers StreamHandlers) (ChatResult, error) {
	sessionID, err := deepSeekCreateSession(ctx, credential.Token)
	if err != nil {
		return ChatResult{}, err
	}
	defer deepSeekDeleteSession(context.WithoutCancel(ctx), credential.Token, sessionID)

	powHeader, err := deepSeekPowHeader(ctx, credential.Token)
	if err != nil {
		return ChatResult{}, err
	}

	body := map[string]any{
		"chat_session_id":  sessionID,
		"parent_message_id": nil,
		"prompt":           deepSeekFlattenPrompt(req),
		"ref_file_ids":     []string{},
		"thinking_enabled": req.Thinking,
		"search_enabled":   false,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ChatResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, deepSeekBaseURL+"/chat/completion", bytes.NewReader(encoded))
	if err != nil {
		return ChatResult{}, err
	}
	for key, value := range deepSeekHeaders(credential.Token, len(encoded)) {
		httpReq.Header.Set(key, value)
	}
	httpReq.Header.Set("X-Ds-Pow-Response", powHeader)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := relayHTTPClient(ctx).Do(httpReq)
	if err != nil {
		return ChatResult{}, newRelayError("", "DeepSeek 对话请求失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet := ""
		if data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096)); readErr == nil {
			snippet = string(data)
		}
		kind := classifyHTTPStatus(resp.StatusCode)
		if kind == "" {
			kind = classifyMessage(snippet)
		}
		return ChatResult{}, newRelayError(kind, "DeepSeek 对话接口返回 %d：%s", resp.StatusCode, truncate(snippet, 300))
	}

	return consumeDeepSeekStream(resp.Body, handlers)
}

// consumeDeepSeekStream 解析 SSE 增量流。
// 设置 WEBRELAY_DEBUG_SSE=1 时把每行原始数据打到 stderr（排障用）。
func consumeDeepSeekStream(reader io.Reader, handlers StreamHandlers) (ChatResult, error) {
	debugSSE := os.Getenv("WEBRELAY_DEBUG_SSE") == "1"
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	var result ChatResult
	var finalContent, snapshotText, snapshotReasoning string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		raw := strings.TrimPrefix(line, "data:")
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "[DONE]" {
			continue
		}
		if debugSSE {
			snippet := raw
			if len(snippet) > 400 {
				snippet = snippet[:400] + "…"
			}
			fmt.Fprintln(os.Stderr, "SSE:", snippet)
		}
		var event struct {
			V   json.RawMessage `json:"v"`
			P   string          `json:"p"`
			// 新版（2026-09 实测）在流末尾给一条顶层最终内容：{"content":"...整段回复..."}
			Content *string `json:"content"`
		}
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			continue
		}
		if event.Content != nil && strings.TrimSpace(*event.Content) != "" {
			finalContent = *event.Content
		}
		if event.P == "" && event.Content == nil {
			// 新版快照事件：{"v":{"response":{"fragments":[{"type":"RESPONSE","content":"..."}]}}}
			var snapshot struct {
				Response struct {
					ThinkingContent *string `json:"thinking_content"`
					Fragments       []struct {
						Type    string `json:"type"`
						Content string `json:"content"`
					} `json:"fragments"`
				} `json:"response"`
			}
			if json.Unmarshal(event.V, &snapshot) == nil {
				for _, fragment := range snapshot.Response.Fragments {
					switch fragment.Type {
					case "RESPONSE":
						if strings.TrimSpace(fragment.Content) != "" {
							snapshotText += fragment.Content
						}
					case "THINKING":
						if strings.TrimSpace(fragment.Content) != "" {
							snapshotReasoning += fragment.Content
						}
					}
				}
			}
			continue
		}
		switch event.P {
		case "response/thinking_content":
			var chunk string
			if json.Unmarshal(event.V, &chunk) == nil && chunk != "" {
				result.Reasoning += chunk
				if handlers.OnReasoning != nil {
					handlers.OnReasoning(chunk)
				}
			}
		case "response/content":
			var chunk string
			if json.Unmarshal(event.V, &chunk) == nil && chunk != "" {
				result.Text += chunk
				if handlers.OnText != nil {
					handlers.OnText(chunk)
				}
			}
		case "response/status":
			// 完成标记。
		}
	}
	if err := scanner.Err(); err != nil {
		return result, newRelayError("", "DeepSeek 流式响应读取失败：%v", err)
	}
	// 新版流：末尾一条顶层最终整段内容最权威；快照次之；旧版逐字增量兜底。
	if strings.TrimSpace(finalContent) != "" {
		result.Text = finalContent
	} else if strings.TrimSpace(result.Text) == "" && strings.TrimSpace(snapshotText) != "" {
		result.Text = snapshotText
	}
	if strings.TrimSpace(result.Reasoning) == "" && strings.TrimSpace(snapshotReasoning) != "" {
		result.Reasoning = snapshotReasoning
	}
	if strings.TrimSpace(result.Text) == "" && strings.TrimSpace(result.Reasoning) == "" {
		return result, newRelayError(FailKindRateLimited, "DeepSeek 没有返回内容（可能被风控或额度耗尽）")
	}
	return result, nil
}
