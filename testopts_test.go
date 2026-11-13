package basecrawler

// testNoProbeOptions 关掉库层默认开启的探测类能力（字典 fuzz、OpenAPI
// spec 探测、路由展开），让功能用例的结果集只取决于被测链路本身。
// 生产默认值由 DefaultOptions 决定，不因测试而改变。
func testNoProbeOptions() *Options {
	o := DefaultOptions()
	o.FuzzPaths = false
	o.ProbeOpenAPI = false
	o.RouteExpand = false
	o.FollowRobotsDisallow = false // 保持旧用例里「Disallow 只输出线索」的断言
	return o
}
