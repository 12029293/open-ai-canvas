package webrelay

import (
	"testing"
)

// PoW 求解器自测：使用公开样例（@rezaparsian/deepseek-pow-solver README）验证 WASM 调用链。
// challenge 是 64 位 hex 目标哈希，difficulty 数万量级，expire_at 为毫秒时间戳。
func TestPowSolverRealSample(t *testing.T) {
	solver, err := getPowSolver()
	if err != nil {
		t.Fatalf("求解器初始化失败：%v", err)
	}
	answer, err := solver.solveChallenge(
		"17f75b5e0984f15fc0b8def0c77b48ee4b41b1865f5e8217971722f7870ad4cb",
		"cba9b8a9bf8368b6341c",
		144000,
		1785354733914,
	)
	if err != nil {
		t.Fatalf("求解失败：%v", err)
	}
	// 前缀一致、确定性算法：样例的已知答案。
	if answer != 32186 {
		t.Fatalf("答案不符：期望 32186，得到 %d", answer)
	}
	header := buildPowHeader("DeepSeekHashV1",
		"17f75b5e0984f15fc0b8def0c77b48ee4b41b1865f5e8217971722f7870ad4cb",
		"cba9b8a9bf8368b6341c", "sig", "/api/v0/chat/completion", answer)
	if header == "" {
		t.Fatal("头部编码为空")
	}
}

func TestPowSolverDeterministic(t *testing.T) {
	solver, err := getPowSolver()
	if err != nil {
		t.Fatalf("求解器初始化失败：%v", err)
	}
	a1, err1 := solver.solveChallenge(
		"17f75b5e0984f15fc0b8def0c77b48ee4b41b1865f5e8217971722f7870ad4cb",
		"cba9b8a9bf8368b6341c", 144000, 1785354733914)
	a2, err2 := solver.solveChallenge(
		"17f75b5e0984f15fc0b8def0c77b48ee4b41b1865f5e8217971722f7870ad4cb",
		"cba9b8a9bf8368b6341c", 144000, 1785354733914)
	if err1 != nil || err2 != nil {
		t.Fatalf("求解失败：%v / %v", err1, err2)
	}
	if a1 != a2 {
		t.Fatalf("同挑战应得到相同答案：%d != %d", a1, a2)
	}
}
