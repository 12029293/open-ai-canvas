package doubao

// 账号池驱动的生成编排：取号 → 生成 → 失败标记并自动切换下一账号重试。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"
)

const maxAccountAttempts = 3

// poolError 区分「账号问题（可切换重试）」与「请求/协议问题（换号无意义）」。
type poolError struct {
	classified *ClassifyError // 非 nil 表示是账号类失败
	err        error
}

func (e *poolError) Error() string {
	if e.classified != nil {
		return e.classified.Message
	}
	return e.err.Error()
}

// ImageRequest 文生图入参。
type ImageRequest struct {
	Prompt string
	Model  string
	Ratio  string
	Style  string
}

// ImageResult 文生图结果。
type ImageResult struct {
	URLs    []string `json:"urls"`
	Account string   `json:"account"`
	Text    string   `json:"text,omitempty"`
}

// VideoRequest 文生视频入参。
// Site 站点偏好：dola 表示只用 Dola 账号（dola- 前缀模型），空 = 豆包优先、Dola 兜底。
type VideoRequest struct {
	Prompt   string
	Model    string
	Duration int
	Ratio    string
	Site     string
}

func pickCredential(s *Service, preferID string) (*ActiveCredential, error) {
	return s.Pick(preferID)
}

// pickWithRetry 取号增强：账号都在短冷却（≤75s）时等冷却恢复再取一次，
// 避免后台任务因一分钟的冷却直接失败。
func pickWithRetry(ctx context.Context, s *Service, prefer string) (*ActiveCredential, error) {
	cred, err := pickCredential(s, prefer)
	if err == nil {
		return cred, nil
	}
	if remain, ok := s.ShortestCooldown(); ok && remain <= 75*time.Second {
		select {
		case <-time.After(remain + 1500*time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if next, retryErr := pickCredential(s, prefer); retryErr == nil {
			return next, nil
		}
	}
	return nil, err
}

func classifyOnce(err error) *poolError {
	var ce *ClassifyError
	if errors.As(err, &ce) {
		return &poolError{classified: ce}
	}
	return &poolError{err: err}
}

// GenerateImageWithPool 从账号池取号生成图片，账号类失败自动切换下一账号。
func GenerateImageWithPool(ctx context.Context, s *Service, req ImageRequest) (*ImageResult, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt 不能为空")
	}
	prefer := ""
	var lastErr error = errors.New("账号池中没有可用账号")
	for attempt := 0; attempt < maxAccountAttempts; attempt++ {
		cred, err := pickWithRetry(ctx, s, prefer)
		if err != nil {
			// 已有真实上游失败时保留原始错误，不被「无号可取」覆盖。
			if attempt > 0 {
				return nil, fmt.Errorf("%s（账号池已无可用账号）", lastErr.Error())
			}
			return nil, err
		}
		// 按取到账号的站点切换域名（豆包 / Dola 同协议双站点），
		// 并注入该账号绑定的出站代理（空 = 直连，覆盖上一轮）。
		ctx = WithOrigin(ctx, siteOrigin(cred.Site))
		ctx = WithProxy(ctx, cred.ProxyURL)
		urls, text, err := GenerateImageOnce(ctx, cred.CookieHeader, req.Prompt, req.Model, req.Ratio, req.Style)
		if err == nil {
			_ = s.MarkSuccess(cred.ID)
			// SSE 下发的图片 URL 全部带水印模板（cthumb_wm1/cpreview_wm1/cdld_wm3），
			// 凭裸 key 走 get_file_url 通道换无水印原片直链；失败时原样返回。
			urls = UpgradeImagesNoWatermark(ctx, cred.CookieHeader, urls)
			return &ImageResult{URLs: urls, Account: cred.Label, Text: text}, nil
		}
		pe := classifyOnce(err)
		lastErr = pe
		if pe.classified == nil {
			// 协议/网络层问题：换号无意义，直接返回
			return nil, pe
		}
		_, _, _ = s.MarkFailed(cred.ID, MarkFailedOptions{
			Kind: pe.classified.Kind, Message: pe.classified.Message,
		})
		prefer = "" // 让池子决定下一个可用账号
	}
	return nil, lastErr
}

// GenerateVideoWithPool 从账号池取号生成视频（同步等待出片，最长约 12 分钟）。
func GenerateVideoWithPool(ctx context.Context, s *Service, req VideoRequest) (*VideoResult, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt 不能为空")
	}
	duration := req.Duration
	if duration <= 0 {
		duration = 10
	}
	// 上游 samantha 服务端实际接受 30s（Dola/豆包网页端 30s 档即由此发出），
	// 时长上限与网页端 5/10/30 档位对齐放开到 30。
	if duration > 30 {
		duration = 30
	}
	if duration < 4 {
		duration = 4
	}
	ratio := strings.TrimSpace(req.Ratio)
	if ratio == "" {
		ratio = "16:9"
	}
	prefer := ""
	site := NormalizeSite(req.Site)
	var lastErr error = errors.New("账号池中没有可用账号")
	for attempt := 0; attempt < maxAccountAttempts; attempt++ {
		var cred *ActiveCredential
		var err error
		if site == SiteDoubao || site == SiteDola {
			// dola- 前缀模型绑定站点：只在指定站点取号，不跨站兜底。
			cred, err = s.PickSite(site, prefer)
		} else {
			cred, err = pickWithRetry(ctx, s, prefer)
		}
		if err != nil {
			// 已有真实上游失败时保留原始错误，不被「无号可取」覆盖。
			if attempt > 0 {
				return nil, fmt.Errorf("%s（账号池已无可用账号）", lastErr.Error())
			}
			return nil, err
		}
		// 按取到账号的站点切换域名（豆包 / Dola 同协议双站点），
		// 并注入该账号绑定的出站代理（空 = 直连，覆盖上一轮）。
		ctx = WithOrigin(ctx, siteOrigin(cred.Site))
		ctx = WithProxy(ctx, cred.ProxyURL)
		result, err := generateVideoOnce(ctx, cred.CookieHeader, req.Prompt, req.Model, duration, ratio)
		if err == nil {
			_ = s.MarkSuccess(cred.ID)
			// Dola 账号有每日视频条数配额，成功出片即累计当日计数。
			if cred.Site == SiteDola {
				_ = s.NoteDolaVideoSuccess(cred.ID)
			}
			// 优先走会话页 fallback_api 通道换无水印母片直链（真无水印，无需遮盖）；
			// 失败再退回 get_play_info/URL 改写的播放地址升级，最终兜底是入库前 delogo。
			if !applyFallbackNoWatermark(ctx, cred.CookieHeader, result) {
				upgradeVideosNoWatermark(ctx, cred.CookieHeader, result)
			}
			result.Account = cred.Label
			return result, nil
		}
		pe := classifyOnce(err)
		lastErr = pe
		if pe.classified == nil {
			return nil, pe
		}
		_, _, _ = s.MarkFailed(cred.ID, MarkFailedOptions{
			Kind: pe.classified.Kind, Message: pe.classified.Message,
		})
		prefer = ""
	}
	return nil, lastErr
}

// generateVideoOnce 单账号完整视频流程：
// 提交(2020) → 已含视频直接返回 → 自动确认（会话绑定） → task_id 轮询 / 会话页轮询。
func generateVideoOnce(ctx context.Context, cookieHeader, prompt, model string, duration int, ratio string) (*VideoResult, error) {
	if strings.TrimSpace(cookieHeader) == "" {
		return nil, errors.New("缺少 Cookie，请重新登录账号")
	}
	localConversationID := fmt.Sprintf("local_%d", rand.Int63n(1e16))
	tabID := randomUUID()
	submitAtMs := time.Now().UnixMilli()

	submitRaw, err := samanthaPost(ctx, cookieHeader, chatCompletionPath, videoPayload(prompt, model, duration, ratio, localConversationID, ""), 7*time.Minute, tabID, "text/event-stream")
	if err != nil {
		var ce *ClassifyError
		if errors.As(err, &ce) {
			return nil, ce
		}
		return nil, err
	}
	submitEvents := ParseSSEEvents(submitRaw)
	submitText := CollectText(submitEvents)
	if block := detectBlock(submitEvents, submitRaw, submitText); block != nil {
		return nil, block
	}

	videos := CollectVideos(submitEvents)
	if len(videos) == 0 {
		videos = CollectVideosLoose(submitRaw)
	}
	taskID := ExtractTaskID(submitEvents, submitRaw)

	// 提交响应恒不回传 conversation_id；用 thread/list 小步重试认出本次新会话。
	convID := ""
	var lastThreadCount int
	for i := 0; i < 4 && convID == ""; i++ {
		if i > 0 {
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		threads := listThreads(ctx, cookieHeader, tabID)
		lastThreadCount = len(threads)
		convID = pickFreshThreadID(threads, submitAtMs, i < 3)
	}
	if convID == "" {
		log.Printf("[doubao] 视频 submit 后未认出新会话：thread/list 共 %d 条，reply=%.200s", lastThreadCount, submitText)
	}

	// 早拍旧片快照：防止把上次任务的迟到视频当成这次的成品。
	known := map[string]bool{}
	if convID != "" {
		if html, err := fetchConversationPage(ctx, cookieHeader, convID, tabID); err == nil {
			for _, v := range ExtractVideosFromPage(html) {
				known[v.URL] = true
			}
		}
	}

	if len(videos) > 0 {
		return &VideoResult{URLs: metaURLs(videos), Message: submitText, TaskID: taskID, ConvID: convID}, nil
	}

	didConfirm := false
	lastText := submitText

	// 自动确认：豆包可能反问「比例/时长确认后生成」。确认必须是 2020+skill17
	// 且绑定同一会话，否则落进空会话永远推进不下去。
	if taskID == "" && convID != "" && isConfirmAsk(lastText) {
		didConfirm = true
		confirmPrompt := fmt.Sprintf("确认，就按 %d 秒、%s 生成。%s。请立即开始生成，不要再询问。", duration, ratio, strings.TrimSpace(prompt))
		confirmRaw, err := samanthaPost(ctx, cookieHeader, chatCompletionPath, videoPayload(confirmPrompt, model, duration, ratio, localConversationID, convID), 7*time.Minute, tabID, "text/event-stream")
		if err == nil {
			confirmEvents := ParseSSEEvents(confirmRaw)
			confirmText := CollectText(confirmEvents)
			if block := detectBlock(confirmEvents, confirmRaw, confirmText); block != nil {
				return nil, block
			}
			lastText = confirmText
			cv := CollectVideos(confirmEvents)
			if len(cv) == 0 {
				cv = CollectVideosLoose(confirmRaw)
			}
			if len(cv) > 0 {
				return &VideoResult{URLs: metaURLs(cv), Message: lastText, ConvID: convID}, nil
			}
			if id := ExtractTaskID(confirmEvents, confirmRaw); id != "" {
				taskID = id
			}
		}
	}

	if taskID == "" {
		if convID == "" {
			if isConfirmAsk(lastText) {
				return nil, fmt.Errorf("豆包在等待参数确认（回复：%s），但自动确认所需的会话未建立，可能上游协议有调整", truncate(lastText, 160))
			}
			return nil, fmt.Errorf("视频任务提交失败：%s", truncate(lastText, 200))
		}
		if !isAcceptance(lastText) && !didConfirm {
			if hardFailurePatterns.MatchString(lastText) {
				return nil, fmt.Errorf("%s", truncate(lastText, 300))
			}
			return nil, fmt.Errorf("视频任务提交失败：%s", truncate(lastText, 200))
		}
		// 已受理但无任务号：轮询会话页面取结果（视频异步写进会话）。
		select {
		case <-time.After(20 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		html, err := fetchConversationPage(ctx, cookieHeader, convID, tabID)
		if err == nil {
			for _, v := range ExtractVideosFromPage(html) {
				known[v.URL] = true
			}
		}
		return pollConversationVideos(ctx, cookieHeader, convID, tabID, known, lastText)
	}

	// 有 task_id：轮询 async/stream。
	deadline := time.Now().Add(20 * time.Minute)
	lastMessage := lastText
	for rounds := 1; time.Now().Before(deadline); rounds++ {
		pollRaw, err := samanthaPost(ctx, cookieHeader, asyncStreamPath, map[string]any{"task_id": taskID, "event_id": 0}, 3*time.Minute, tabID, "text/event-stream")
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		pollEvents := ParseSSEEvents(pollRaw)
		if t := CollectText(pollEvents); t != "" {
			lastMessage = t
		}
		if block := detectBlock(pollEvents, pollRaw, lastMessage); block != nil {
			return nil, block
		}
		if hardFailurePatterns.MatchString(lastMessage) {
			return nil, fmt.Errorf("%s", truncate(lastMessage, 300))
		}
		videos := CollectVideos(pollEvents)
		if len(videos) == 0 {
			videos = CollectVideosLoose(pollRaw)
		}
		if len(videos) > 0 {
			return &VideoResult{URLs: metaURLs(preferWatermarkFree(videos)), Message: lastMessage, TaskID: taskID, ConvID: convID}, nil
		}
		delay := time.Duration(3+rounds) * time.Second
		if delay > 10*time.Second {
			delay = 10 * time.Second
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("%s视频已提交但等待超时，请稍后在豆包网页端查看，或重试", prefixText(lastMessage))
}

func prefixText(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	return truncate(t, 160) + "；"
}

func metaURLs(list []videoMeta) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, v.URL)
	}
	return out
}

// pollConversationVideos 轮询会话页面直到出现新视频。
func pollConversationVideos(ctx context.Context, cookieHeader, convID, tabID string, known map[string]bool, lastText string) (*VideoResult, error) {
	// 实测豆包 Seedance 出片可达 15-20 分钟，轮询窗口给足余量。
	deadline := time.Now().Add(20 * time.Minute)
	for rounds := 1; time.Now().Before(deadline); rounds++ {
		html, err := fetchConversationPage(ctx, cookieHeader, convID, tabID)
		if err == nil && html != "" {
			var fresh []videoMeta
			for _, v := range ExtractVideosFromPage(html) {
				if !known[v.URL] {
					fresh = append(fresh, v)
				}
			}
			if len(fresh) > 0 {
				return &VideoResult{URLs: metaURLs(preferWatermarkFree(fresh)), Message: lastText, ConvID: convID}, nil
			}
		}
		delay := time.Duration(10+rounds*2) * time.Second
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("%s已等待出片但会话里仍未出现视频，视频可能仍在生成，请稍后在豆包网页端该会话查看，或重试", prefixText(lastText))
}
