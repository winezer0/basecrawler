package main

import (
	"strings"
	"testing"
)

// TestVersionStringFormat 默认版本为 dev，格式为 "dev (commit <vcs.time>)"。
func TestVersionStringFormat(t *testing.T) {
	got := versionString()
	if !strings.HasPrefix(got, "dev (commit ") {
		t.Errorf("格式应为 dev (commit ...), got %q", got)
	}
	if !strings.HasSuffix(got, ")") {
		t.Errorf("应以 ) 结尾: %q", got)
	}
	commit := got[len("dev (commit ") : len(got)-1]
	if commit == "" {
		t.Error("commit 部分不应为空")
	}
	// 仓库内构建有 vcs.time；剥离或仓库外构建为 unknown——两者都合法
	if commit != "unknown" && len(commit) < 10 {
		t.Errorf("vcs.time 形态可疑: %q", commit)
	}
}

// TestMaybePrintVersion 只有 -version 为 true 时才短路退出。
func TestMaybePrintVersion(t *testing.T) {
	if maybePrintVersion(&cliConfig{}) {
		t.Error("Version=false 不应短路")
	}
	if !maybePrintVersion(&cliConfig{Version: true}) {
		t.Error("Version=true 应短路")
	}
}

// TestParseArgsVersionFlag --version 注册且解析正确（多字符 flag 为 GNU 长名）。
func TestParseArgsVersionFlag(t *testing.T) {
	for _, arg := range []string{"--version"} {
		cfg, err := parseArgs([]string{"-u", "https://a.example/", arg}, &strings.Builder{})
		if err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
		if !cfg.Version {
			t.Errorf("%s 应置 Version=true", arg)
		}
	}
}
