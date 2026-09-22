package webrelay

// DeepSeek 网页版完成请求需要 x-ds-pow-response 头：向 /api/v0/chat/create_pow_challenge
// 拿挑战，再用 DeepSeek 前端自带的 SHA3 WASM 模块求解。
// 这里内嵌该 WASM（sha3_wasm_bg.7b9ca65ddd.wasm，来自 chat.deepseek.com 前端产物），
// 用纯 Go 的 wazero 运行时执行，调用约定与参考实现（wasmtime/wasmer 侧）一致：
//   prefix = salt + "_" + expire_at + "_"
//   answer = wasm_solve(challenge, prefix, difficulty)
//   头部内容 = base64(JSON{algorithm, challenge, salt, answer, signature, target_path})

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed wasm/sha3_wasm_bg.wasm
var powWasmBinary []byte

type powSolver struct {
	mu      sync.Mutex
	runtime wazero.Runtime
	module  api.Module
	memory  api.Memory
	alloc   api.Function
	stack   api.Function
	solve   api.Function
	// difficulty 是否为 f64 参数（f32 时写 float32）。
	difficultyIsFloat64 bool
}

var (
	solverOnce sync.Once
	solverRef  *powSolver
	solverErr  error
)

// getPowSolver 进程级单例（模块无跨请求状态，可并发复用；内部调用已加锁）。
func getPowSolver() (*powSolver, error) {
	solverOnce.Do(func() {
		solverRef, solverErr = newPowSolver()
	})
	return solverRef, solverErr
}

func newPowSolver() (*powSolver, error) {
	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig())
	compiled, err := runtime.CompileModule(ctx, powWasmBinary)
	if err != nil {
		return nil, fmt.Errorf("编译 PoW WASM 失败：%w", err)
	}
	// wasm-bindgen 产物可能声明占位导入；为所有缺失导入注入返回零值的桩。
	if err := stubImports(ctx, runtime, compiled); err != nil {
		return nil, err
	}
	instance, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("deepseek_pow"))
	if err != nil {
		return nil, fmt.Errorf("实例化 PoW WASM 失败：%w", err)
	}
	memory := instance.ExportedMemory("memory")
	alloc := instance.ExportedFunction("__wbindgen_export_0")
	stack := instance.ExportedFunction("__wbindgen_add_to_stack_pointer")
	solve := instance.ExportedFunction("wasm_solve")
	if memory == nil || alloc == nil || stack == nil || solve == nil {
		return nil, errors.New("PoW WASM 缺少必要导出（memory / __wbindgen_export_0 / __wbindgen_add_to_stack_pointer / wasm_solve）")
	}
	solver := &powSolver{runtime: runtime, module: instance, memory: memory, alloc: alloc, stack: stack, solve: solve}
	if definition := solve.Definition(); definition != nil {
		for _, paramType := range definition.ParamTypes() {
			if paramType == api.ValueTypeF64 {
				solver.difficultyIsFloat64 = true
			}
		}
	}
	return solver, nil
}

// stubImports 给编译模块的全部导入建立空实现宿主模块（wasm-bindgen 产物的占位导入不会被求解路径触达）。
func stubImports(ctx context.Context, runtime wazero.Runtime, compiled wazero.CompiledModule) error {
	type functionKey struct{ module, name string }
	signatures := map[functionKey][2][]api.ValueType{}
	for _, imported := range compiled.ImportedFunctions() {
		moduleName, name, _ := imported.Import()
		signatures[functionKey{moduleName, name}] = [2][]api.ValueType{imported.ParamTypes(), imported.ResultTypes()}
	}
	modules := map[string][]functionKey{}
	for key := range signatures {
		modules[key.module] = append(modules[key.module], key)
	}
	for moduleName, keys := range modules {
		builder := runtime.NewHostModuleBuilder(moduleName)
		for _, key := range keys {
			signature := signatures[key]
			builder.NewFunctionBuilder().
				WithGoFunction(api.GoFunc(func(ctx context.Context, stack []uint64) {}), signature[0], signature[1]).
				Export(key.name)
		}
		if _, err := builder.Instantiate(ctx); err != nil {
			return fmt.Errorf("注入 PoW WASM 导入桩失败（%s）：%w", moduleName, err)
		}
	}
	return nil
}

// solveChallenge 求解挑战，返回 answer（nonce）。
func (p *powSolver) solveChallenge(challenge string, salt string, difficulty int, expireAt int64) (int64, error) {
	if p == nil {
		return 0, errors.New("PoW 求解器不可用")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx := context.Background()
	prefix := fmt.Sprintf("%s_%d_", salt, expireAt)

	// 与参考实现一致：先 __wbindgen_add_to_stack_pointer(-16) 拿返回区指针，结束后 +16 归还。
	stackResults, err := p.stack.Call(ctx, uint64(uint32(0xFFFFFFF0)))
	if err != nil {
		return 0, fmt.Errorf("PoW 栈指针调整失败：%w", err)
	}
	if len(stackResults) == 0 {
		return 0, errors.New("PoW 栈指针调用无返回")
	}
	stackPointer := int32(uint32(stackResults[0]))
	defer func() {
		_, _ = p.stack.Call(ctx, uint64(uint32(16)))
	}()

	challengePtr, err := p.writeString(ctx, challenge)
	if err != nil {
		return 0, err
	}
	prefixPtr, err := p.writeString(ctx, prefix)
	if err != nil {
		return 0, err
	}

	var args []uint64
	if p.difficultyIsFloat64 {
		args = []uint64{uint64(uint32(stackPointer)), uint64(uint32(challengePtr)), uint64(len(challenge)), uint64(uint32(prefixPtr)), uint64(len(prefix)), math.Float64bits(float64(difficulty))}
	} else {
		args = []uint64{uint64(uint32(stackPointer)), uint64(uint32(challengePtr)), uint64(len(challenge)), uint64(uint32(prefixPtr)), uint64(len(prefix)), uint64(math.Float32bits(float32(difficulty)))}
	}
	if _, err := p.solve.Call(ctx, args...); err != nil {
		return 0, fmt.Errorf("PoW 求解执行失败：%w", err)
	}

	statusBytes, ok := p.memory.Read(uint32(stackPointer), 4)
	if !ok {
		return 0, errors.New("PoW 结果读取失败")
	}
	status := int32(binary.LittleEndian.Uint32(statusBytes))
	if status == 0 {
		return 0, errors.New("PoW 求解未得到答案（难度过高或挑战无效）")
	}
	valueBytes, ok := p.memory.Read(uint32(stackPointer+8), 8)
	if !ok {
		return 0, errors.New("PoW 答案读取失败")
	}
	return int64(math.Float64frombits(binary.LittleEndian.Uint64(valueBytes))), nil
}

func (p *powSolver) writeString(ctx context.Context, text string) (uint32, error) {
	encoded := []byte(text)
	results, err := p.alloc.Call(ctx, uint64(len(encoded)), 1)
	if err != nil {
		return 0, fmt.Errorf("PoW 内存分配失败：%w", err)
	}
	if len(results) == 0 {
		return 0, errors.New("PoW 内存分配无返回")
	}
	ptr := uint32(results[0])
	if !p.memory.Write(ptr, encoded) {
		return 0, errors.New("PoW 内存写入失败")
	}
	return ptr, nil
}

// buildPowHeader 生成 x-ds-pow-response 头。
func buildPowHeader(algorithm string, challenge string, salt string, signature string, targetPath string, answer int64) string {
	payload := fmt.Sprintf(`{"algorithm":"%s","challenge":"%s","salt":"%s","signature":"%s","answer":%d,"target_path":"%s"}`,
		algorithm, challenge, salt, signature, answer, targetPath)
	return base64.StdEncoding.EncodeToString([]byte(payload))
}

// solvePowChallenge 取挑战并求解，返回 x-ds-pow-response 头的值。
func solvePowChallenge(algorithm string, challenge string, salt string, difficulty int, expireAt int64, signature string, targetPath string) (string, error) {
	solver, err := getPowSolver()
	if err != nil {
		return "", err
	}
	answer, err := solver.solveChallenge(strings.TrimSpace(challenge), salt, difficulty, expireAt)
	if err != nil {
		return "", err
	}
	return buildPowHeader(algorithm, challenge, salt, signature, targetPath, answer), nil
}
