package basecrawler

import "sort"

// orderTasks 在每一层内做稳定分区：静态资源与常规链接在前，
// 「可以晚一点抓」的页面类任务在后。
//
// 背景：一个 Nuxt 首页的 __NUXT_DATA__ 能给出 200 条站内文档路径。若它们
// 与同层 JS/CSS 一起抢 MaxURLs 配额，实测 JS 提取结果从 62 条掉到 16 条——
// 新增能力反而挤掉了原有召回。sitemap 当年是同类问题，当时的解法是
// 「引导任务延后并入」（bootstrap.go）；这里用「同层内稳定分区」解决。
//
// 只分区、不排序：workers 并发汇总出的顺序本身不具语义，稳定分区足够，
// 也不会改变既有任务的相对次序。
func orderTasks(in []task) []task {
	primary, deferred := partitionTasks(in)
	return append(primary, deferred...)
}

// orderTasksStable 在分区之后把每个分区的顺序确定下来。
//
// 背景：runLevel 由多个 worker 并发汇总，任务到达顺序依赖网络时序。配额饱和时
// （vuejs.org 的 -max-urls 300 就是这种情形），「前 300 条是谁」每次都不同，
// 于是同二进制连跑会出现 17 增 / 17 失的子域轮换，基线失去可比性。
//
// 做法是两段纯函数：先按 host 分组并对组内按 URL 排序，再按 host 字母序轮转。
// 轮转保证每个 host 的前几条都能进队列，不会被某一个子域整批挤掉；
// 排序保证顺序只由任务集合决定，与并发完成顺序无关。
// 分区语义不变：deferPage 仍然整体排在资源类之后。
func orderTasksStable(in []task) []task {
	primary, deferred := partitionTasks(in)
	return append(interleaveHosts(primary), interleaveHosts(deferred)...)
}

// partitionTasks 只分区、不排序：workers 汇总出的顺序本身不具语义，
// 这也是 orderTasks 保持稳定的原因。
func partitionTasks(in []task) ([]task, []task) {
	if len(in) < 2 {
		if len(in) == 1 && in[0].deferPage {
			return []task{}, in
		}
		return in, []task{}
	}
	out := make([]task, 0, len(in))
	deferred := make([]task, 0, len(in)/2)
	for _, t := range in {
		if t.deferPage {
			deferred = append(deferred, t)
			continue
		}
		out = append(out, t)
	}
	// primary 内部：boost（分页链下一页）排最前。插入排序，集合小。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].boost && !out[j-1].boost; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, deferred
}

func taskURL(t task) string {
	if t.url == nil {
		return ""
	}
	return t.url.String()
}

func taskHost(t task) string {
	if t.url == nil {
		return ""
	}
	return t.url.Hostname()
}

// interleaveHosts 按 host 轮转合并，组内按 URL 字符串稳定排序。
// 纯函数：同样的输入集合永远得到同样的输出顺序。
func interleaveHosts(in []task) []task {
	if len(in) < 2 {
		return in
	}
	groups := make(map[string][]task, 8)
	for _, t := range in {
		h := taskHost(t)
		groups[h] = append(groups[h], t)
	}
	hosts := make([]string, 0, len(groups))
	for h := range groups {
		hosts = append(hosts, h)
		sort.SliceStable(groups[h], func(i, j int) bool {
			return taskURL(groups[h][i]) < taskURL(groups[h][j])
		})
	}
	// host 顺序也必须是确定的，但不能用纯字典序：主域往往任务最多，
	// 按字母排会把它放到最后，配额饱和时主域资产反而被挤掉。
	// 用「任务数降序 + 字典序兜底」既确定，又保留主域优先的语义。
	sort.Slice(hosts, func(i, j int) bool {
		ni, nj := len(groups[hosts[i]]), len(groups[hosts[j]])
		if ni != nj {
			return ni > nj
		}
		return hosts[i] < hosts[j]
	})

	out := make([]task, 0, len(in))
	for round := 0; len(out) < len(in); round++ {
		progressed := false
		for _, h := range hosts {
			if round < len(groups[h]) {
				out = append(out, groups[h][round])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
}
