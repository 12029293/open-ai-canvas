package doubao

// 会话级轨迹：定位「上游受理但不出片」到底卡在哪个环节。
// 轨迹随 VideoResult 成功返回、随 VideoFailure 失败透传，app 层落 task_logs。

import (
	"encoding/json"
	"time"
)

// VideoStage 轨迹中的单个环节事件。
type VideoStage struct {
	At     time.Time `json:"at"`
	Stage  string    `json:"stage"`
	Detail string    `json:"detail,omitempty"`
}

// VideoTrace 一次视频生成的完整会话轨迹（提交 → 受理 → 轮询 → 出片/失败）。
type VideoTrace struct {
	Site    string       `json:"site,omitempty"`
	Account string       `json:"account,omitempty"`
	Model   string       `json:"model,omitempty"`
	Started time.Time    `json:"started"`
	Stages  []VideoStage `json:"stages"`
}

func newVideoTrace(model string) *VideoTrace {
	return &VideoTrace{Model: model, Started: time.Now()}
}

func (t *VideoTrace) Add(stage, detail string) {
	if t == nil {
		return
	}
	t.Stages = append(t.Stages, VideoStage{At: time.Now(), Stage: stage, Detail: detail})
}

// JSON 压缩序列化；超长截断，避免撑爆 task_logs.payload 与后端日志。
func (t *VideoTrace) JSON() string {
	if t == nil {
		return ""
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	return truncate(string(raw), 4000)
}

// VideoFailure 携带会话轨迹的失败。Error() 与底层错误保持一致，
// 分类/换号逻辑经 errors.As 解包不受影响，诊断层单独取 Trace。
type VideoFailure struct {
	Err   error
	Trace *VideoTrace
}

func (e *VideoFailure) Error() string { return e.Err.Error() }
func (e *VideoFailure) Unwrap() error { return e.Err }
