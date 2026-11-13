package main

import (
	"bytes"
	"testing"
)

// TestParseArgsSafetyDefaults：安全链默认开启、发送上限有默认值、hidden 默认占位。
func TestParseArgsSafetyDefaults(t *testing.T) {
	cfg, err := parseArgs([]string{"-u", "https://x/"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.Safety == nil || !cfg.Safety.Enabled() {
		t.Error("safety 应默认开启")
	}
	if cfg.MaxTestFill != 200 {
		t.Errorf("默认发送上限 = %d，期望 200", cfg.MaxTestFill)
	}
	if cfg.UseHiddenValues {
		t.Error("hidden 静态值默认应为占位符")
	}
}

// TestParseArgsSafetyOverrides：关闭防护、自定义上限、追加规则、使用原值。
func TestParseArgsSafetyOverrides(t *testing.T) {
	cfg, err := parseArgs([]string{
		"-u", "https://x/", "--no-safety", "--max-testfill", "7",
		"--testfill-use-hidden", "--safety-url-skip", "/secret", "--safety-form-skip", "wipe",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.Safety.Enabled() {
		t.Error("--no-safety 应关闭拦截")
	}
	if cfg.MaxTestFill != 7 || !cfg.UseHiddenValues {
		t.Errorf("覆盖参数未接线: max=%d hidden=%v", cfg.MaxTestFill, cfg.UseHiddenValues)
	}
	// 重新开启时追加规则应生效
	cfg2, err := parseArgs([]string{"-u", "https://x/", "--safety-url-skip", "/secret"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg2.Safety.SkipURL("https://x/secret/panel") {
		t.Error("--safety-url-skip 追加规则未生效")
	}
}

// TestParseArgsSafetyBadRegex：非法正则在解析阶段报错，不留半防护状态。
func TestParseArgsSafetyBadRegex(t *testing.T) {
	for _, args := range [][]string{
		{"-u", "https://x/", "--safety-url-skip", "((("},
		{"-u", "https://x/", "--safety-form-skip", "*bad"},
		{"-u", "https://x/", "--safety-action-skip", "[bad"},
	} {
		if _, err := parseArgs(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v 应报错", args[2:])
		}
	}
}

// TestParseArgsSafetyActionSkip：第三类词表（非幂等动作）从 CLI 可达。
func TestParseArgsSafetyActionSkip(t *testing.T) {
	cfg, err := parseArgs([]string{
		"-u", "https://x/", "--safety-action-skip", "wipe_?table",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if skip, _ := cfg.Safety.SkipRequest("POST", "https://x/db", []string{"wipe_table"}); !skip {
		t.Error("--safety-action-skip 追加规则未生效")
	}
	// 动作词表只在非幂等方法上生效，GET 不误拦
	if skip, _ := cfg.Safety.SkipRequest("GET", "https://x/db", []string{"wipe_table"}); skip {
		t.Error("动作词表不应影响幂等方法")
	}
}
