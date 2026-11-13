package main

import (
	"bytes"
	"testing"
)

// TestParseArgsActiveOptions：库层边界字段在 CLI 的默认值与覆盖映射。
func TestParseArgsActiveOptions(t *testing.T) {
	cfg, err := parseArgs([]string{"-u", "https://x/"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.Opts.FollowFormActions {
		t.Error("FollowFormActions 应默认关闭")
	}
	if !cfg.Opts.AllowUnsafeSend {
		t.Error("AllowUnsafeSend 应默认开启")
	}

	cfg2, err := parseArgs([]string{
		"-u", "https://x/", "--follow-form-actions", "--no-unsafe-send",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg2.Opts.FollowFormActions {
		t.Error("--follow-form-actions 未接线")
	}
	if cfg2.Opts.AllowUnsafeSend {
		t.Error("--no-unsafe-send 未接线")
	}
}
