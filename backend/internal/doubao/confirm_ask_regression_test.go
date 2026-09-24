package doubao

import "testing"

// 用上游实测回复原文回归 isConfirmAsk / isAcceptance：
// 选择题式追问必须触发自动确认，受理话术绝不能误判为追问。
func TestIsConfirmAskRealReplies(t *testing.T) {
	choiceAsk := "这个表述还差一个关键参数：时长。目前支持 4～15 秒视频。\n\n请选择一个方向，我再生成：\n\n1. **4秒**：适合做动态片头 / 短循环画面  \n2. **8秒**：适合展示一个完整动作或氛围片段  \n3. **15秒**：适合更完整的小剧情 / 产品展示 / 氛围短片  \n\n另外，“中年的视频”我理解可能是指：  \nA. 中年人物主题  \nB. 电影感中年氛围  \nC. 中年生活场景  \n\n你回我类似：**“8秒，中年人物主题”** 或 **“15秒，电影感中年氛围”** 即可。"
	if !isConfirmAsk(choiceAsk) {
		t.Fatalf("选择题式追问应触发自动确认")
	}

	acceptance := "正在为您生成一个15秒的青年主题视频。\n本次使用 **Seedance 2.0 Mini** 生成，预计等待 10 分钟。视频生成好后，我会主动发送给你。本次生成将消耗每日免费额度。"
	if isConfirmAsk(acceptance) {
		t.Fatalf("受理话术不应被当作追问")
	}
	if !isAcceptance(acceptance) {
		t.Fatalf("受理话术应被识别为已受理")
	}
}
