package webrelay

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// probe 尝试一组调用变体，dump retptr 前 16 字节状态。
func probe(t *testing.T, p *powSolver, challenge string, prefix string, difficulty float32) {
	ctx := context.Background()
	stackResults, err := p.stack.Call(ctx, uint64(uint32(0xFFFFFFF0)))
	if err != nil {
		t.Logf("probe stack err: %v", err)
		return
	}
	retptr := int32(uint32(stackResults[0]))
	defer func() { _, _ = p.stack.Call(ctx, uint64(uint32(16))) }()
	cp, _ := p.writeString(ctx, challenge)
	pp, _ := p.writeString(ctx, prefix)
	args := []uint64{uint64(uint32(retptr)), uint64(uint32(cp)), uint64(len(challenge)), uint64(uint32(pp)), uint64(len(prefix)), uint64(math.Float32bits(difficulty))}
	if _, err := p.solve.Call(ctx, args...); err != nil {
		t.Logf("probe call err: %v", err)
		return
	}
	dump, _ := p.memory.Read(uint32(retptr), 16)
	status := int32(binary.LittleEndian.Uint32(dump[0:4]))
	value := math.Float64frombits(binary.LittleEndian.Uint64(dump[8:16]))
	t.Logf("challenge=%q prefix=%q diff=%v -> status=%d f64@8=%v raw=% x", challenge, prefix, difficulty, status, value, dump)
}

func TestPowProbeVariants(t *testing.T) {
	solver, err := getPowSolver()
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	solver.mu.Lock()
	defer solver.mu.Unlock()

	probe(t, solver, "dGVzdC1jaGFsbGVuZ2U=", "testsalt_1730000000_", 4)
	probe(t, solver, "dGVzdC1jaGFsbGVuZ2U=", "testsalt_1730000000", 4)
	probe(t, solver, "AAAAAAAAAAAAAAAAAAAAAA==", "salt_100_", 1)
	probe(t, solver, "", "", 1)
	probe(t, solver, "dGVzdA==", "salt_1_", 0)
	_ = fmt.Sprint()
}
