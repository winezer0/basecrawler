package extractor

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"io"
	"strings"
)

// 这里只做「字节 → 结构化数据」的解析，不负责抓取。
// 抓取由 fetcher 完成，什么时候抓由编排层决定。

// RobotsInfo 是从 robots.txt 中提取的结构化信息。
type RobotsInfo struct {
	Sitemaps []string
	Disallow []string
	Allow    []string
}

// ParseRobots 解析 robots.txt。
//
// 这里只做解析，不决定是否抓取：
//   - Sitemap 声明的地址由编排层加入抓取队列（站点明确提供给爬虫的）
//   - Disallow / Allow 由编排层输出为线索；API 发现定位下 Disallow 还会按
//     Options.FollowRobotsDisallow 入队抓取，关闭时退回「只给线索」。
//
// 合规边界放在编排层而不是解析器：安全测试里 Disallow 的 /admin、/backup
// 往往最值钱，是否主动访问应由使用者基于书面授权决定，并可用
// --no-robots-disallow 一键关闭。
func ParseRobots(body []byte) RobotsInfo {
	var info RobotsInfo
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:i]))
		val := strings.TrimSpace(line[i+1:])
		if val == "" {
			continue
		}
		switch key {
		case "sitemap":
			info.Sitemaps = append(info.Sitemaps, val)
		case "disallow":
			if val != "/" { // 全站禁止没有线索价值
				info.Disallow = append(info.Disallow, val)
			}
		case "allow":
			info.Allow = append(info.Allow, val)
		}
	}
	return info
}

// sitemapDoc 覆盖 sitemap index 与 urlset 两种格式。
// 刻意不给 XMLName 加 tag：零值表示接受任意根节点名，
// 否则根节点名不匹配时 Unmarshal 会直接失败。
type sitemapDoc struct {
	XMLName xml.Name
	URLs    []sitemapNode `xml:"url"`
	Index   []sitemapNode `xml:"sitemap"`
}

type sitemapNode struct {
	Loc string `xml:"loc"`
}

// ParseSitemap 解析 sitemap.xml，返回其中的页面地址与嵌套的 sitemap 地址。
func ParseSitemap(body []byte) (urls []string, sitemaps []string) {
	// 有些站点的 sitemap.xml 实际是 gzip 压缩文件（.xml.gz 或直接伪装成 .xml）
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			defer zr.Close()
			if raw, err := io.ReadAll(io.LimitReader(zr, 32<<20)); err == nil {
				body = raw
			}
		}
	}

	var doc sitemapDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, nil
	}
	for _, n := range doc.URLs {
		if loc := strings.TrimSpace(n.Loc); loc != "" {
			urls = append(urls, loc)
		}
	}
	for _, n := range doc.Index {
		if loc := strings.TrimSpace(n.Loc); loc != "" {
			sitemaps = append(sitemaps, loc)
		}
	}
	return urls, sitemaps
}
