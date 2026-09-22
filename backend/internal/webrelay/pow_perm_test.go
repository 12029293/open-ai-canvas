package webrelay

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
)

func probeHex(t *testing.T, p *powSolver, label string, challenge string, prefix string, difficulty float64) {
	ctx := context.Background()
	stackResults, err := p.stack.Call(ctx, uint64(uint32(0xFFFFFFF0)))
	if err != nil {
		t.Logf("%s stack err: %v", label, err)
		return
	}
	retptr := int32(uint32(stackResults[0]))
	defer func() { _, _ = p.stack.Call(ctx, uint64(uint32(16))) }()
	cp, _ := p.writeString(ctx, challenge)
	pp, _ := p.writeString(ctx, prefix)
	args := []uint64{uint64(uint32(retptr)), uint64(uint32(cp)), uint64(len(challenge)), uint64(uint32(pp)), uint64(len(prefix)), math.Float64bits(difficulty)}
	if _, err := p.solve.Call(ctx, args...); err != nil {
		t.Logf("%s call err: %v", label, err)
		return
	}
	dump, _ := p.memory.Read(uint32(retptr), 16)
	status := int32(binary.LittleEndian.Uint32(dump[0:4]))
	value := math.Float64frombits(binary.LittleEndian.Uint64(dump[8:16]))
	t.Logf("%s -> status=%d answer=%v", label, status, value)
}

func TestPowHexChallenge(t *testing.T) {
	solver, err := getPowSolver()
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	solver.mu.Lock()
	defer solver.mu.Unlock()

	probeHex(t, solver, "hex01,diff4", "01", "salt_1_", 4)
	probeHex(t, solver, "hex01,diff8", "01", "salt_1_", 8)
	probeHex(t, solver, "hexffff,diff12", "ffff", "testsalt_1730000000_", 12)
	probeHex(t, solver, "hexlong,diff16", "0123456789abcdef", "testsalt_1730000000_", 16)
}
