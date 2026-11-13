package main

import (
	"fmt"
	"runtime/debug"
)

// version 是发布版本号，默认为 dev。发布构建由链接器覆盖：
//
//	go build -ldflags "-X main.version=v0.2.0" -buildvcs=true ./cmd/assetcrawler
//
// 设计参照 tanspec 的 CLI 版本方案：构建时注入（发布工作流传版本号、
// 本地构建保持 dev），不写死源码、不依赖 git 标签（发布构建发生在
// 打 tag 之前，本地无标签检出也不可靠）。
var version = "dev"

// versionString 返回 "version (commit <vcs.time>)"。
// vcs.time 是当前修订时间（不是编译时刻），来自 Go 构建信息——
// -buildvcs=auto 在仓库内构建时默认嵌入；仓库外构建或剥离后为 unknown。
func versionString() string {
	return fmt.Sprintf("%s (commit %s)", version, vcsTime())
}

// vcsTime 从构建信息读 vcs.time，缺失时返回 unknown。
func vcsTime() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.time" {
			if s.Value == "" {
				return "unknown"
			}
			return s.Value
		}
	}
	return "unknown"
}

// maybePrintVersion 处理 -version：输出版本并返回 true 表示调用方应立即退出。
// 查询路径不加载配置、不建抓取器、不发任何请求。
func maybePrintVersion(cfg *cliConfig) bool {
	if !cfg.Version {
		return false
	}
	fmt.Println(versionString())
	return true
}
