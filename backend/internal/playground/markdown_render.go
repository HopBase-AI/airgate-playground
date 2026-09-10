package playground

import (
	"bytes"
	"fmt"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// ── 文档渲染:Markdown → 打印友好 HTML ────────────────────────────────────────
// 三层防注入:goldmark 默认转义 raw HTML(不开 WithUnsafe)→ bluemonday UGC
// 白名单清洗 → html/template 组装外壳。HTML 只作 PDF 中间态,不落资产、
// 不出站内 URL(防存储型 XSS)。

var docMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

// docSanitizer 显式允许清单(不含 img——JS 已禁,但子资源加载不受禁 JS 约束,
// img src 会成为对 internal 网络内服务的 SSRF/信标向量)。
var docSanitizer = func() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"h1", "h2", "h3", "h4", "h5", "h6",
		"p", "br", "hr", "blockquote",
		"ul", "ol", "li",
		"strong", "em", "del", "s", "sup", "sub",
		"table", "thead", "tbody", "tr", "th", "td",
		"pre", "code", "span",
	)
	policy.AllowAttrs("class").OnElements("code", "pre", "span")
	policy.AllowAttrs("align").OnElements("th", "td")
	policy.AllowAttrs("start").OnElements("ol")
	policy.AllowAttrs("checked", "disabled", "type").OnElements("input") // GFM 任务列表
	policy.AllowElements("input")
	policy.AllowStandardURLs()
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowElements("a")
	policy.RequireNoFollowOnLinks(true)
	return policy
}()

// docShellTemplate A4 打印外壳:中文字体链(边车镜像内置 Noto CJK)、
// 表格/代码块样式、分页规则。
var docShellTemplate = template.Must(template.New("doc").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>
  * { box-sizing: border-box; }
  body {
    font-family: 'Noto Sans CJK SC', 'Noto Sans SC', 'PingFang SC', 'Helvetica Neue', Arial, sans-serif;
    font-size: 11pt;
    line-height: 1.65;
    color: #1e293b;
    margin: 0;
  }
  h1 { color: #0f172a; font-size: 20pt; margin: 0 0 0.6em; border-bottom: 3px solid #2563eb; padding-bottom: 0.3em; }
  h2 { color: #2563eb; font-size: 15pt; margin: 1.2em 0 0.5em; }
  h3 { font-size: 12.5pt; margin: 1em 0 0.4em; }
  h2, h3, h4 { page-break-after: avoid; }
  p { margin: 0.5em 0; }
  pre {
    background: #f6f6f4;
    border: 1px solid #e4e4e0;
    border-radius: 6px;
    padding: 10px 12px;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 9.5pt;
    page-break-inside: auto;
  }
  code { font-family: 'Noto Sans Mono CJK SC', 'SF Mono', Menlo, Consolas, monospace; font-size: 0.92em; }
  p code, li code { background: #f2f2ef; border-radius: 4px; padding: 1px 4px; }
  table { border-collapse: collapse; width: 100%; margin: 0.8em 0; page-break-inside: auto; font-size: 10pt; }
  thead { display: table-header-group; }
  tr { page-break-inside: avoid; page-break-after: auto; }
  th, td { border: 1px solid #d4d4d0; padding: 6px 9px; text-align: left; vertical-align: top; }
  th { background: #e8eef8; color: #1e3a8a; font-weight: 600; }
  blockquote { border-left: 3px solid #d0d0cc; margin: 0.6em 0; padding: 0.1em 0 0.1em 12px; color: #555; }
  ul, ol { padding-left: 1.6em; margin: 0.5em 0; }
  hr { border: none; border-top: 1px solid #e0e0e0; margin: 1.2em 0; }
  a { color: #1259a6; text-decoration: none; }
  img { max-width: 100%; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
{{.Body}}
</body>
</html>`))

// renderDocumentHTML 把 Markdown 报告渲染成自包含打印 HTML。
func renderDocumentHTML(title string, markdown []byte) ([]byte, error) {
	var converted bytes.Buffer
	if err := docMarkdown.Convert(markdown, &converted); err != nil {
		return nil, fmt.Errorf("markdown render failed: %w", err)
	}
	safe := docSanitizer.SanitizeBytes(converted.Bytes())

	var out bytes.Buffer
	err := docShellTemplate.Execute(&out, struct {
		Title string
		Body  template.HTML
	}{
		Title: title,
		// 已经过 bluemonday 白名单清洗,可作为受信 HTML 注入外壳
		Body: template.HTML(safe), //nolint:gosec
	})
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
