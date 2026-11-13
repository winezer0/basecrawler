package routegraph

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestExecBudgetCoversParseAndTransform：预算必须从 goja.Parse /
// esbuild 转译就开始算。给 1ms 预算、2MB 级 ESM 源码，
// 调用方不能被解析/转译阶段拖住。
func TestExecBudgetCoversParseAndTransform(t *testing.T) {
	src := []byte(strings.Repeat("export const x = 1;\n", 100000))
	start := time.Now()
	_, err := ExtractByExec(src, ExecOptions{Timeout: time.Millisecond})
	if err == nil {
		t.Fatal("1ms 预算下不应完成解析+转译+执行")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("外层 wall-clock 未生效，耗时 %v", d)
	}
}

// TestExecBudgetIncludesVMRun：模块 init 死循环时，vm.Interrupt 与
// 外层 deadline 都应在预算内返回，而不是挂死调用方。
func TestExecBudgetIncludesVMRun(t *testing.T) {
	src := `(self.webpackChunkapp=self.webpackChunkapp||[]).push([["routes"],{500:function(m,e,r){while(true){}}}]);`
	start := time.Now()
	_, err := ExtractByExec([]byte(src), ExecOptions{Timeout: 60 * time.Millisecond})
	if err == nil {
		t.Fatal("死循环 chunk 应超时")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("VM 段未被预算兜住，耗时 %v", d)
	}
}

// TestExecSkipAndSizeGuards 覆盖执行兜底的三个提前返回：体积上限、
// 非自注册 chunk、ESM 转译失败；调用方据此静默跳过而不是报错刷屏。
func TestExecSkipAndSizeGuards(t *testing.T) {
	if _, err := ExtractByExec([]byte(strings.Repeat("a", 100)),
		ExecOptions{MaxSourceBytes: 10}); err == nil {
		t.Error("超过 MaxSourceBytes 应直接拒绝")
	}
	for _, src := range []string{
		`var x = 1;`,      // CJS 解析成功但非自注册 chunk
		`import { broken`, // ESM 语法本身非法，转译也失败
	} {
		if _, err := ExtractByExec([]byte(src), ExecOptions{}); !errors.Is(err, ErrSkipExec) {
			t.Errorf("%q 应返回 ErrSkipExec，得到 %v", src, err)
		}
	}
}

// TestESMTransformAndExpiredDeadline 覆盖 ESM 转译的成功/失败，
// 以及转译后执行段在 deadline 已耗尽时立即返回。
func TestESMTransformAndExpiredDeadline(t *testing.T) {
	if cjs, err := esmToCJS([]byte(`export const a = 1;`)); err != nil || len(cjs) == 0 {
		t.Errorf("合法 ESM 应转译成功，得到 cjs=%q err=%v", cjs, err)
	}
	if _, err := esmToCJS([]byte(`function (`)); err == nil {
		t.Error("非法 JS 应返回转译错误")
	}
	if _, err := extractByExecESM([]byte(`var x = 1;`),
		ExecOptions{Timeout: time.Millisecond}, time.Now().Add(-time.Second)); err == nil {
		t.Error("deadline 已耗尽应返回超时错误")
	}
}
