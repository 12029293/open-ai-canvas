package webrelay

import (
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func TestPowDebugDump(t *testing.T) {
	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig())
	compiled, err := runtime.CompileModule(ctx, powWasmBinary)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	t.Log("== imports ==")
	for _, imp := range compiled.ImportedFunctions() {
		moduleName, name, _ := imp.Import()
		t.Logf("import %s.%s params=%v results=%v", moduleName, name, imp.ParamTypes(), imp.ResultTypes())
	}
	t.Log("== exports ==")
	for name, def := range compiled.ExportedFunctions() {
		t.Logf("export %s params=%v results=%v", name, def.ParamTypes(), def.ResultTypes())
	}

	solver, err := getPowSolver()
	if err != nil {
		t.Fatalf("solver init: %v", err)
	}
	_ = api.ValueTypeI32

	// 手动复现调用，dump 返回区内存。
	p := solver
	p.mu.Lock()
	defer p.mu.Unlock()

	challenge := "test-challenge"
	prefix := "testsalt_1730000000_"

	stackResults, err := p.stack.Call(ctx, uint64(uint32(0xFFFFFFF0)))
	if err != nil {
		t.Fatalf("stack: %v", err)
	}
	retptr := int32(uint32(stackResults[0]))
	t.Logf("retptr=%d", retptr)
	defer func() { _, _ = p.stack.Call(ctx, uint64(uint32(16))) }()

	cp, err := p.writeString(ctx, challenge)
	if err != nil {
		t.Fatalf("write challenge: %v", err)
	}
	pp, err := p.writeString(ctx, prefix)
	if err != nil {
		t.Fatalf("write prefix: %v", err)
	}
	t.Logf("challenge_ptr=%d len=%d prefix_ptr=%d len=%d", cp, len(challenge), pp, len(prefix))

	args := []uint64{uint64(uint32(retptr)), uint64(uint32(cp)), uint64(len(challenge)), uint64(uint32(pp)), uint64(len(prefix)), math.Float64bits(float64(4))}
	_, err = p.solve.Call(ctx, args...)
	if err != nil {
		t.Fatalf("solve call: %v", err)
	}
	dump, _ := p.memory.Read(uint32(retptr), 32)
	if dump != nil {
		t.Logf("retptr dump: % x", dump)
		t.Logf("i32@0=%d f64@8=%f i64@8=%d", int32(binary.LittleEndian.Uint32(dump[0:4])), math.Float64frombits(binary.LittleEndian.Uint64(dump[8:16])), int64(binary.LittleEndian.Uint64(dump[8:16])))
	}
}
