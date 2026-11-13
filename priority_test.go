package basecrawler

import (
	"net/url"
	"testing"

	"github.com/winezer0/basecrawler/extractor"
)

func priorityTask(p, source string, deferred bool) task {
	return task{url: &url.URL{Path: p}, source: source, deferPage: deferred}
}

func TestOrderTasksKeepsAssetsFirst(t *testing.T) {
	in := []task{
		priorityTask("/app.js", "script", false),
		priorityTask("/docs/1", "inline-json:nuxt-data", true),
		priorityTask("/style.css", "link", false),
		priorityTask("/docs/2", "inline-json:nuxt-data", true),
	}
	got := orderTasks(in)

	want := []string{"/app.js", "/style.css", "/docs/1", "/docs/2"}
	if len(got) != len(want) {
		t.Fatalf("长度 %d，期望 %d", len(got), len(want))
	}
	for i, p := range want {
		if got[i].url.Path != p {
			t.Errorf("位置 %d = %s，期望 %s（实际 %v）", i, got[i].url.Path, p, got)
		}
	}
}

func TestOrderTasksWithoutDeferredKeepsOrder(t *testing.T) {
	in := []task{
		priorityTask("/a.js", "script", false),
		priorityTask("/b.css", "link", false),
	}
	got := orderTasks(in)
	if got[0].url.Path != "/a.js" || got[1].url.Path != "/b.css" {
		t.Errorf("顺序被改变: %v", got)
	}
}

func TestOrderTasksEmpty(t *testing.T) {
	if got := orderTasks(nil); len(got) != 0 {
		t.Errorf("nil 输入应返回空，实际 %v", got)
	}
}

// TestToTasksDefersInlineJSON 锁定「提取层 → 调度层」的接线，
// 防止新增载体只改了 extractor 却忘了标记优先级。
func TestToTasksDefersInlineJSON(t *testing.T) {
	c, err := New(testNoProbeOptions())
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://example.com/")
	c.frontier.AddSeed(base)
	tasks := c.toTasks([]extractor.Request{
		{Raw: "/app.js", Source: "script", Method: "GET"},
		{Raw: "/docs/1", Source: "inline-json:nuxt-data", Method: "GET"},
	}, base, 1, "https://example.com/")
	if len(tasks) != 2 {
		t.Fatalf("期望 2 个任务，实际 %d", len(tasks))
	}
	if tasks[0].deferPage || !tasks[1].deferPage {
		t.Errorf("deferPage 标记错误: %+v / %+v", tasks[0], tasks[1])
	}
	if got := orderTasks(tasks); got[0].url.Path != "/app.js" {
		t.Errorf("资源应排在页面之前，实际 %s", got[0].url.Path)
	}
}

func hostedTask(raw, source string, deferred bool) task {
	u, _ := url.Parse(raw)
	return task{url: u, source: source, deferPage: deferred}
}

func taskOrder(in []task) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		out = append(out, taskURL(t))
	}
	return out
}

// TestOrderTasksStableIsDeterministic 锁定「同样的集合、不同的到达顺序」输出一致。
// 这正是配额饱和时连跑差集要收敛的原因。
func TestOrderTasksStableIsDeterministic(t *testing.T) {
	mk := func(reverse bool) []task {
		in := []task{
			hostedTask("https://b.example.com/2.js", "script", false),
			hostedTask("https://a.example.com/3.css", "link", false),
			hostedTask("https://b.example.com/1.js", "script", false),
			hostedTask("https://a.example.com/1.css", "link", false),
			hostedTask("https://c.example.com/x.js", "script", false),
		}
		if reverse {
			for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
				in[i], in[j] = in[j], in[i]
			}
		}
		return in
	}
	a := taskOrder(orderTasksStable(mk(false)))
	b := taskOrder(orderTasksStable(mk(true)))
	if len(a) != len(b) {
		t.Fatalf("长度不同: %v / %v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("顺序不确定: %v / %v", a, b)
		}
	}
}

func TestOrderTasksStableInterleavesHosts(t *testing.T) {
	in := []task{
		hostedTask("https://a.example.com/1.js", "script", false),
		hostedTask("https://a.example.com/2.js", "script", false),
		hostedTask("https://a.example.com/3.js", "script", false),
		hostedTask("https://b.example.com/1.js", "script", false),
	}
	got := taskOrder(orderTasksStable(in))
	want := []string{
		"https://a.example.com/1.js",
		"https://b.example.com/1.js",
		"https://a.example.com/2.js",
		"https://a.example.com/3.js",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("轮转结果 = %v，期望 %v", got, want)
		}
	}
}

func TestOrderTasksStableKeepsDeferredLast(t *testing.T) {
	in := []task{
		hostedTask("https://a.example.com/page", "inline-json:nuxt-data", true),
		hostedTask("https://a.example.com/app.js", "script", false),
		hostedTask("https://b.example.com/doc", "inline-json:nuxt-data", true),
		hostedTask("https://b.example.com/main.css", "link", false),
	}
	got := orderTasksStable(in)
	if len(got) != 4 {
		t.Fatalf("长度 %d", len(got))
	}
	if got[2].deferPage != true || got[3].deferPage != true {
		t.Errorf("页面类应整体排后: %v", taskOrder(got))
	}
}

func TestOrderTasksUnstableKeepsArrivalOrder(t *testing.T) {
	in := []task{
		hostedTask("https://a.example.com/2.js", "script", false),
		hostedTask("https://a.example.com/1.js", "script", false),
	}
	got := taskOrder(orderTasks(in))
	if got[0] != "https://a.example.com/2.js" {
		t.Errorf("旧行为应保留到达顺序，实际 %v", got)
	}
}

// TestPartitionTasksBoost 分页链下一页（boost）排 primary 最前。
func TestPartitionTasksBoost(t *testing.T) {
	in := []task{
		priorityTask("/data1", "json", false),
		priorityTask("/next", "json-pagination", false),
		priorityTask("/data2", "json", false),
	}
	in[1].boost = true
	primary, deferred := partitionTasks(in)
	if len(deferred) != 0 {
		t.Fatalf("无 deferPage 应全在 primary, got %d deferred", len(deferred))
	}
	if len(primary) != 3 {
		t.Fatalf("应 3 条 primary, got %d", len(primary))
	}
	if primary[0].source != "json-pagination" {
		t.Errorf("boost 任务应排最前, got %s", primary[0].source)
	}
	// boost 与 deferPage 共存时仍以 deferPage 分区优先
	in2 := []task{
		priorityTask("/next", "json-pagination", true),
		priorityTask("/data", "json", false),
	}
	in2[0].boost = true
	primary2, deferred2 := partitionTasks(in2)
	if len(deferred2) != 1 || deferred2[0].source != "json-pagination" {
		t.Errorf("deferPage 应压过 boost: primary=%d deferred=%d", len(primary2), len(deferred2))
	}
}

// TestSourcemapDeferred 锁定 Q-3：sourcemap sources 归一化后一次能产出
// 上百条构建机路径（O-4 实测单站 202 条），若与同层 JS/CSS 抢配额会
// 挤掉主资源召回（与 inline-json 同类）。归入延后分区，live 与否由
// 请求决定——不因 live 率低而丢弃（以获取到 URL 资源为准）。
// 判定条件在 crawler.toTasks 的 deferPage 表达式里（见该处注释）。
func TestSourcemapDeferred(t *testing.T) {
	u, _ := url.Parse("https://s.com/src/a.ts")
	g, _ := url.Parse("https://s.com/docs/intro")
	tasks := []task{
		{url: u, source: "sourcemap", attribute: "sources", tag: "sourcemap",
			deferPage: shouldDeferPage("sourcemap", "sources", false)},
		{url: g, source: "js", tag: "script",
			deferPage: shouldDeferPage("js", "", false)},
	}
	primary, deferred := partitionTasks(tasks)
	if len(primary) != 1 || primary[0].source != "js" {
		t.Fatalf("主资源应在 primary: %+v", primary)
	}
	if len(deferred) != 1 || deferred[0].source != "sourcemap" {
		t.Fatalf("sourcemap 应延后: %+v", deferred)
	}
}
