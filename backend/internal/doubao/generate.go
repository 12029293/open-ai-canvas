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

// maxAccountAttempts 单任务最多尝试的账号数：连接类失败（代理挂掉）现在
// 也会换号重试，给到 5 次以覆盖「个别账号代理不可用」的池子状态；
// 失败账号会进 60s 冷却，重试自然落到其他账号。
const maxAccountAttempts = 5

// poolError 区分「账号问题（可切换重试）」与「请求/协议问题（换号无意义）」。
type poolError struct {
	classified *ClassifyError // 非 nil 表示是账号类失败
	err        error          // 原始错误（可能含 VideoFailure 等包装），供诊断层 errors.As 解包
}

// Unwrap 保留完整错误链：轨迹（VideoFailure）随 classified 分类丢失会导致
// 任务日志缺会话轨迹，这里让 errors.As 能穿透 poolError 拿到底层。
func (e *poolError) Unwrap() error { return e.err }

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
	// OnAccountPicked 每次从账号池取到账号后回调（换号重试会多次触发），
	// app 层用它把当前账号名回填到任务记录。
	OnAccountPicked func(site, label string)
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
	// OnAccountPicked 每次从账号池取到账号后回调（换号重试会多次触发），
	// app 层用它把当前账号名回填到任务记录。
	OnAccountPicked func(site, label string)
}

func pickCredential(s *Service, preferID string) (*ActiveCredential, error) {
	return s.Pick(preferID)
}

// busyWaitIdleAccounts 账号全忙时的最长等待时长：超时后按原错误返回，
// 避免任务无限占住 worker。
const busyWaitIdleAccounts = 10 * time.Minute

// pickWithRetry 取号增强：
//  1. 账号全忙（都被并行任务占用）时周期性重试，等空闲账号——超出账号数的
//     任务由此自然排队轮转，而不是直接失败；
//  2. 账号都在短冷却（≤75s）时等冷却恢复再取一次，避免后台任务因一分钟的冷却直接失败。
func pickWithRetry(ctx context.Context, s *Service, prefer string) (*ActiveCredential, error) {
	cred, err := pickCredential(s, prefer)
	if err == nil {
		return cred, nil
	}
	if errors.Is(err, ErrAllAccountsBusy) {
		deadline := time.Now().Add(busyWaitIdleAccounts)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
			}
			cred, err = pickCredential(s, prefer)
			if err == nil {
				return cred, nil
			}
			if !errors.Is(err, ErrAllAccountsBusy) {
				break
			}
		}
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

// pickSiteWithRetry 指定站点取号：全忙时等待空闲账号（与 pickWithRetry 的忙等一致）。
func pickSiteWithRetry(ctx context.Context, s *Service, site, prefer string) (*ActiveCredential, error) {
	cred, err := s.PickSite(site, prefer)
	if err == nil {
		return cred, nil
	}
	if errors.Is(err, ErrAllAccountsBusy) {
		deadline := time.Now().Add(busyWaitIdleAccounts)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
			}
			cred, err = s.PickSite(site, prefer)
			if err == nil {
				return cred, nil
			}
			if !errors.Is(err, ErrAllAccountsBusy) {
				break
			}
		}
	}
	return nil, err
}

func classifyOnce(err error) *poolError {
	var ce *ClassifyError
	if errors.As(err, &ce) {
		return &poolError{classified: ce, err: err}
	}
	return &poolError{err: err}
}

// markFailedOptionsFor 把分类错误转成账号池失败标记；周期性额度耗尽时冷却
// 到上游声明的恢复时刻（不足 30 分钟按 30 分钟，其余分类走默认策略）。
func markFailedOptionsFor(ce *ClassifyError) MarkFailedOptions {
	opts := MarkFailedOptions{Kind: ce.Kind, Message: ce.Message}
	if d := time.Until(ce.CooldownUntil); d > 0 {
		if ms := d.Milliseconds(); ms > CooldownQuotaMs {
			opts.CooldownMs = ms
		}
	}
	return opts
}

// maybeOpenManualVerify 710022004（滑块/安全验证）只能人工过：判定命中时
// 自动弹出预注入该账号 Cookie 的验证窗口（已有验证会话时不重复开窗）。
// 启动失败（如无浏览器）不影响生成主流程，错误只留在验证会话状态里。
func maybeOpenManualVerify(s *Service, accountID string, ce *ClassifyError) {
	if !IsVerifySceneError(ce) {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		_, _, _ = s.StartManualVerify(accountID)
	}()
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
				return nil, fmt.Errorf("%s（已换号重试，池内无其他可用账号）", lastErr.Error())
			}
			return nil, err
		}
		if req.OnAccountPicked != nil {
			req.OnAccountPicked(cred.Site, cred.Label)
		}
		// 生成在闭包内完成：无论成功、失败还是换号，都释放账号占用，
		// 让并行任务能立刻领到空闲账号。
		res, pe := func() (*ImageResult, *poolError) {
			defer s.ReleaseAccount(cred.ID)
			// 按取到账号的站点切换域名（豆包 / Dola 同协议双站点），
			// 并注入该账号绑定的出站代理（空 = 直连）与登录浏览器指纹。
			taskCtx := WithOrigin(ctx, siteOrigin(cred.Site))
			taskCtx = WithProxy(taskCtx, cred.ProxyURL)
			taskCtx = WithFingerprint(taskCtx, cred.Fingerprint())
			urls, text, err := GenerateImageOnce(taskCtx, cred.CookieHeader, req.Prompt, req.Model, req.Ratio, req.Style)
			if err == nil {
				_ = s.MarkSuccess(cred.ID)
				// SSE 下发的图片 URL 全部带水印模板（cthumb_wm1/cpreview_wm1/cdld_wm3），
				// 凭裸 key 走 get_file_url 通道换无水印原片直链；失败时原样返回。
				urls = UpgradeImagesNoWatermark(taskCtx, cred.CookieHeader, urls)
				return &ImageResult{URLs: urls, Account: cred.Label, Text: text}, nil
			}
			pe := classifyOnce(err)
			if pe.classified != nil {
				_, _, _ = s.MarkFailed(cred.ID, MarkFailedOptions{
					Kind: pe.classified.Kind, Message: pe.classified.Message,
				})
				maybeOpenManualVerify(s, cred.ID, pe.classified)
			}
			return nil, pe
		}()
		if pe == nil {
			return res, nil
		}
		lastErr = pe
		if pe.classified == nil {
			// 协议/网络层问题：换号无意义，直接返回
			return nil, pe
		}
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
			cred, err = pickSiteWithRetry(ctx, s, site, prefer)
		} else {
			cred, err = pickWithRetry(ctx, s, prefer)
		}
		if err != nil {
			// 已有真实上游失败时保留原始错误，不被「无号可取」覆盖。
			if attempt > 0 {
				return nil, fmt.Errorf("%s（已换号重试，池内无其他可用账号）", lastErr.Error())
			}
			return nil, err
		}
		if req.OnAccountPicked != nil {
			req.OnAccountPicked(cred.Site, cred.Label)
		}
		// 生成在闭包内完成：无论成功、失败还是换号，都释放账号占用，
		// 让并行任务能立刻领到空闲账号。
		result, pe := func() (*VideoResult, *poolError) {
			defer s.ReleaseAccount(cred.ID)
			// 按取到账号的站点切换域名（豆包 / Dola 同协议双站点），
			// 并注入该账号绑定的出站代理（空 = 直连）与登录浏览器指纹。
			taskCtx := WithOrigin(ctx, siteOrigin(cred.Site))
			taskCtx = WithProxy(taskCtx, cred.ProxyURL)
			taskCtx = WithFingerprint(taskCtx, cred.Fingerprint())
			result, err := generateVideoOnce(taskCtx, cred.CookieHeader, req.Prompt, req.Model, duration, ratio)
			if err == nil {
				_ = s.MarkSuccess(cred.ID)
				// Dola 账号有每日视频条数配额，成功出片即累计当日计数。
				if cred.Site == SiteDola {
					_ = s.NoteDolaVideoSuccess(cred.ID)
				}
				// 优先走会话页 fallback_api 通道换无水印母片直链（真无水印，无需遮盖）；
				// 失败再退回 get_play_info/URL 改写的播放地址升级，最终兜底是入库前 delogo。
				if !applyFallbackNoWatermark(taskCtx, cred.CookieHeader, result) {
					upgradeVideosNoWatermark(taskCtx, cred.CookieHeader, result)
				}
				result.Account = cred.Label
				if result.Trace != nil {
					result.Trace.Site = cred.Site
					result.Trace.Account = cred.Label
				}
				return result, nil
			}
			pe := classifyOnce(err)
			if pe.classified != nil {
				_, _, _ = s.MarkFailed(cred.ID, markFailedOptionsFor(pe.classified))
				maybeOpenManualVerify(s, cred.ID, pe.classified)
			}
			// 失败轨迹记到后端日志：站点/账号 + 全程环节，定位卡点用。
			var vf *VideoFailure
			if errors.As(err, &vf) && vf.Trace != nil {
				vf.Trace.Site = cred.Site
				vf.Trace.Account = cred.Label
				log.Printf("[doubao] 视频会话轨迹 site=%s account=%s trace=%s", cred.Site, cred.Label, vf.Trace.JSON())
			}
			return nil, pe
		}()
		if pe == nil {
			return result, nil
		}
		lastErr = pe
		if pe.classified == nil {
			return nil, pe
		}
		prefer = ""
	}
	return nil, lastErr
}

// generateVideoOnce 单账号完整视频流程：
// 提交(2020) → 已含视频直接返回 → 自动确认（会话绑定） → task_id 轮询 / 会话页轮询。
// 全程记录会话轨迹：成功挂在 VideoResult.Trace，失败包成 VideoFailure 透传。
func generateVideoOnce(ctx context.Context, cookieHeader, prompt, model string, duration int, ratio string) (res *VideoResult, err error) {
	trace := newVideoTrace(model)
	defer func() {
		if err != nil {
			err = &VideoFailure{Err: err, Trace: trace}
		} else if res != nil {
			res.Trace = trace
		}
	}()
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
		trace.Add("submit_block", block.Kind+": "+truncate(block.Message, 160))
		return nil, enrichQuotaCooldown(block, submitText)
	}

	videos := CollectVideos(submitEvents)
	if len(videos) == 0 {
		videos = CollectVideosLoose(submitRaw)
	}
	taskID := ExtractTaskID(submitEvents, submitRaw)
	trace.Add("submit_reply", fmt.Sprintf("taskID=%s videos=%d text=%.160s", taskID, len(videos), submitText))

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
	trace.Add("conv", fmt.Sprintf("convID=%s threads=%d", convID, lastThreadCount))

	// 早拍旧片快照：防止把上次任务的迟到视频当成这次的成品。
	known := map[string]bool{}
	if convID != "" {
		if html, err := fetchConversationPage(ctx, cookieHeader, convID, tabID); err == nil {
			for _, v := range ExtractVideosFromPage(html) {
				known[v.URL] = true
			}
		}
	}
	trace.Add("known_snapshot", fmt.Sprintf("videos=%d", len(known)))

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
				trace.Add("confirm_block", block.Kind+": "+truncate(block.Message, 160))
				return nil, enrichQuotaCooldown(block, confirmText)
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
			// 追问/拒绝文案都不是终态（实测先追问、先拒绝，视频随后仍写进
			// 会话页）：给一个短宽限窗只扫会话页等晚到视频，仍无片才把豆包
			// 回复当失败原因。不立即判负，避免错杀实际已出片的任务。
			return waitLateVideo(ctx, cookieHeader, convID, tabID, known, lastText, trace, 6*time.Minute)
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
		return pollConversationVideos(ctx, cookieHeader, convID, tabID, known, lastText, trace)
	}

	// 有 task_id：轮询 async/stream。
	deadline := time.Now().Add(20 * time.Minute)
	lastMessage := lastText
	lastTraceText := lastText
	lastBlockMsg := ""
	rejectionMsg := ""
	pageScans := 0
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
		if lastMessage != lastTraceText {
			// 上游回复文本变化是会话推进的关键信号（受理话术 → 生成中 → 完成/拦截）。
			lastTraceText = lastMessage
			trace.Add("poll_text", truncate(lastMessage, 160))
		}
		// 周期性额度耗尽（"近 7 天的额度用完了…预计9月28日恢复"）是终态回复：
		// 任务不会再推进，立即失败并把账号冷却到恢复时刻，避免白等 20 分钟。
		if weeklyQuotaPatterns.MatchString(lastMessage) {
			ce := &ClassifyError{
				Kind:    FailKindQuotaExhausted,
				Message: truncate("周期额度已用完："+lastMessage, 300),
			}
			if until, ok := weeklyQuotaRecoveryAt(lastMessage); ok {
				ce.CooldownUntil = until
			}
			return nil, ce
		}
		if block := detectBlock(pollEvents, pollRaw, lastMessage); block != nil {
			// 生成已在上游受理：轮询阶段的限流/额度文本只是本次查询被拦，
			// 任务本身仍在推进——降速续轮询，直到出片或超时；只有登录态
			// 失效才中止（Cookie 失效后继续轮询不可能拿到结果）。
			// 之前一遇限流就判失败，导致豆包侧实际出片、任务却拿不到视频。
			if block.Kind == FailKindSessionExpired {
				return nil, block
			}
			if block.Message != lastBlockMsg {
				lastBlockMsg = block.Message
				trace.Add("poll_block", block.Kind+": "+truncate(block.Message, 120))
			}
			log.Printf("[doubao] 轮询阶段被拦（%s），继续等待出片", block.Message)
			select {
			case <-time.After(15 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		// 先收视频、后判拒绝：拒绝文案与成片可能在同一轮回包里，
		// 一旦本轮已带视频就按成功返回，绝不能让拒绝判定抢先生效。
		videos := CollectVideos(pollEvents)
		if len(videos) == 0 {
			videos = CollectVideosLoose(pollRaw)
		}
		if len(videos) > 0 {
			trace.Add("stream_video", fmt.Sprintf("round=%d count=%d", rounds, len(videos)))
			return &VideoResult{URLs: metaURLs(preferWatermarkFree(videos)), Message: lastMessage, TaskID: taskID, ConvID: convID}, nil
		}
		// 轮询回复命中"生成失败/违规"等文案不是终态（实测先拒绝后仍出片，
		// 且裸词"违规"会误伤"侵权/违规内容"拒绝文案）：只记录、降速续轮询，
		// 直到出片或超时；超时未出片才把拒绝文案作为失败原因上报。
		if hardFailurePatterns.MatchString(lastMessage) {
			if rejectionMsg == "" {
				rejectionMsg = truncate(lastMessage, 200)
				trace.Add("poll_reject_seen", rejectionMsg)
			}
			log.Printf("[doubao] 轮询回复疑似拒绝（%s），继续等待出片", truncate(lastMessage, 120))
			select {
			case <-time.After(10 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		// 混合轮询：async/stream 沉默不代表没出片——定期扫会话页兜底命中。
		if convID != "" && rounds%6 == 0 {
			pageScans++
			if html, perr := fetchConversationPage(ctx, cookieHeader, convID, tabID); perr == nil && html != "" {
				// 拒绝文案不是终态（实测先拒绝后仍出片）：只记录，超时未出片才上报。
				if msg := pageRejectionMessage(html); msg != "" && rejectionMsg == "" {
					rejectionMsg = msg
					trace.Add("page_reject_seen", truncate(msg, 160))
				}
				var fresh []videoMeta
				for _, v := range ExtractVideosFromPage(html) {
					if !known[v.URL] {
						fresh = append(fresh, v)
					}
				}
				trace.Add("poll_page_scan", fmt.Sprintf("round=%d html=%d fresh=%d", rounds, len(html), len(fresh)))
				if len(fresh) > 0 {
					trace.Add("page_video", fmt.Sprintf("round=%d count=%d", rounds, len(fresh)))
					return &VideoResult{URLs: metaURLs(preferWatermarkFree(fresh)), Message: lastMessage, TaskID: taskID, ConvID: convID}, nil
				}
			} else {
				trace.Add("poll_page_scan", fmt.Sprintf("round=%d error=%v", rounds, perr))
			}
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
	trace.Add("stream_window_exhausted", fmt.Sprintf("page_scans=%d last_text=%.120s", pageScans, lastMessage))
	// 流式窗口耗尽：迟到的成片常在超时前后写入会话页，继续只扫会话页再等
	// 一段窗口（worker 视频超时默认 60 分钟，豆包层总耗时仍在其内）。
	if convID != "" {
		pageDeadline := time.Now().Add(20 * time.Minute)
		scans := 0
		for time.Now().Before(pageDeadline) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			html, err := fetchConversationPage(ctx, cookieHeader, convID, tabID)
			scans++
		if err == nil && html != "" {
			// 拒绝文案不是终态（实测先拒绝后仍出片）：只记录，超时未出片才上报。
			if msg := pageRejectionMessage(html); msg != "" && rejectionMsg == "" {
				rejectionMsg = msg
				trace.Add("page_reject_seen", truncate(msg, 160))
			}
				var fresh []videoMeta
				for _, v := range ExtractVideosFromPage(html) {
					if !known[v.URL] {
						fresh = append(fresh, v)
					}
				}
				if scans%6 == 1 {
					trace.Add("page_wait", fmt.Sprintf("scan=%d html=%d fresh=%d", scans, len(html), len(fresh)))
				}
				if len(fresh) > 0 {
					trace.Add("page_video", fmt.Sprintf("scan=%d count=%d", scans, len(fresh)))
					return &VideoResult{URLs: metaURLs(preferWatermarkFree(fresh)), Message: lastMessage, TaskID: taskID, ConvID: convID}, nil
				}
			} else {
				trace.Add("page_wait", fmt.Sprintf("scan=%d error=%v", scans, err))
			}
			delay := 30 * time.Second
			if scans%4 == 0 {
				delay = 45 * time.Second
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	trace.Add("timeout", truncate(lastMessage, 160))
	if rejectionMsg != "" {
		return nil, fmt.Errorf("%s豆包曾回复「%s」但等待超时仍未出片；若提示词含版权角色、真人肖像等敏感内容，请调整后重试", prefixText(lastMessage), rejectionMsg)
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

// pageRejectionMessage 会话页出现内容安全拒绝文案（"疑似包含侵权/违规内容…
// 生成额度未扣除"）时返回提示语。实测该文案不是终态：豆包可能先回拒绝、
// 随后仍把视频生成出来，因此调用方只记录、不中止——等待全程结束仍未出片
// 时，才把它作为失败原因上报。拒绝是提示词级判定，不算账号失败，不冷却、
// 不换号。
func pageRejectionMessage(html string) string {
	if contentRejectPatterns.MatchString(html) {
		return "豆包提示内容疑似侵权/违规（生成额度未扣除）"
	}
	return ""
}

// waitLateVideo 豆包回了追问/拒绝文字且没有任务号时的宽限等待：实测这类
// 回复不是终态，视频可能仍异步写进会话页。窗口内每 20 秒扫一次会话页，
// 出片即返回；到点仍无片才以 doubaoText 为失败原因（普通错误，不算账号失败）。
func waitLateVideo(ctx context.Context, cookieHeader, convID, tabID string, known map[string]bool, doubaoText string, trace *VideoTrace, window time.Duration) (*VideoResult, error) {
	deadline := time.Now().Add(window)
	scans := 0
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		html, err := fetchConversationPage(ctx, cookieHeader, convID, tabID)
		scans++
		if err == nil && html != "" {
			var fresh []videoMeta
			for _, v := range ExtractVideosFromPage(html) {
				if !known[v.URL] {
					fresh = append(fresh, v)
				}
			}
			if scans%8 == 1 {
				trace.Add("late_wait", fmt.Sprintf("scan=%d html=%d fresh=%d", scans, len(html), len(fresh)))
			}
			if len(fresh) > 0 {
				trace.Add("late_video", fmt.Sprintf("scan=%d count=%d", scans, len(fresh)))
				return &VideoResult{URLs: metaURLs(preferWatermarkFree(fresh)), Message: doubaoText, ConvID: convID}, nil
			}
		} else if scans%8 == 1 {
			trace.Add("late_wait", fmt.Sprintf("scan=%d error=%v", scans, err))
		}
		select {
		case <-time.After(20 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	trace.Add("late_timeout", truncate(doubaoText, 160))
	return nil, fmt.Errorf("视频任务提交失败：%s", truncate(doubaoText, 300))
}

// pollConversationVideos 轮询会话页面直到出现新视频。
func pollConversationVideos(ctx context.Context, cookieHeader, convID, tabID string, known map[string]bool, lastText string, trace *VideoTrace) (*VideoResult, error) {
	// 实测豆包 Seedance 出片可达 15-20 分钟；15 秒长档豆包自称"预计等待 10
	// 分钟"但实际更慢，20 分钟窗口曾多次在出片前耗尽（任务在 ~21 分钟失败）。
	// 放宽到 30 分钟；单账号最坏耗时 submit(≤7min)+本窗口(30min)≈37min，
	// 仍在 worker 视频超时（默认 60 分钟）之内。
	deadline := time.Now().Add(30 * time.Minute)
	rejectionMsg := ""
	for rounds := 1; time.Now().Before(deadline); rounds++ {
		html, err := fetchConversationPage(ctx, cookieHeader, convID, tabID)
		if err == nil && html != "" {
			if msg := pageRejectionMessage(html); msg != "" && rejectionMsg == "" {
				rejectionMsg = msg
				trace.Add("page_reject_seen", truncate(msg, 160))
			}
			var fresh []videoMeta
			for _, v := range ExtractVideosFromPage(html) {
				if !known[v.URL] {
					fresh = append(fresh, v)
				}
			}
			// 轨迹只记关键点：首轮、每 12 轮、命中，避免撑爆 payload。
			if rounds == 1 || rounds%12 == 0 || len(fresh) > 0 {
				trace.Add("page_scan", fmt.Sprintf("round=%d html=%d fresh=%d", rounds, len(html), len(fresh)))
			}
			if len(fresh) > 0 {
				return &VideoResult{URLs: metaURLs(preferWatermarkFree(fresh)), Message: lastText, ConvID: convID}, nil
			}
		} else if rounds%12 == 1 {
			trace.Add("page_scan", fmt.Sprintf("round=%d error=%v", rounds, err))
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
	trace.Add("page_timeout", truncate(lastText, 120))
	if rejectionMsg != "" {
		return nil, fmt.Errorf("%s豆包曾提示内容疑似侵权/违规，且等待超时仍未出片，请调整提示词后重试", prefixText(lastText))
	}
	return nil, fmt.Errorf("%s已等待出片但会话里仍未出现视频，视频可能仍在生成，请稍后在豆包网页端该会话查看，或重试", prefixText(lastText))
}
