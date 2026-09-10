package playground

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// ── PDF 渲染(chromedp → headless-shell 边车) ────────────────────────────────
// 边车跑在 compose 的 internal 网络(无外网出口),配合禁 JS + 白名单清洗后的
// HTML,构成三层防护。边车不可用时调用方明确返回 PDF 生成错误。

type pdfRenderer struct {
	cdpURL  string
	sem     chan struct{}
	timeout time.Duration
}

func newPDFRenderer(cdpURL string) *pdfRenderer {
	cdpURL = strings.TrimRight(strings.TrimSpace(cdpURL), "/")
	if cdpURL == "" {
		return nil
	}
	return &pdfRenderer{
		cdpURL:  cdpURL,
		sem:     make(chan struct{}, 2), // 并发上限 2,防边车内存被打爆
		timeout: 45 * time.Second,
	}
}

// resolveCDPURL 把 cdpURL 里的 docker 服务名解析成容器 IP。
//
// 新版 Chrome 对 DevTools 的 /json HTTP 端点做 DNS-rebinding 防护:Host 头只放行
// IP 与 localhost 字面量,用服务名(如 http://chromium:9222)访问会被 500 拒,导致
// 健康探测误判"不可达"。改用 IP 访问则 /json 与后续 wsUrl 全程
// 不撞校验。每次调用重新解析,边车容器重建换 IP 也能跟上;已是 IP/localhost 或解析
// 失败时原样返回(后续探测/连接自然失败并返回错误)。
func (r *pdfRenderer) resolveCDPURL(ctx context.Context) string {
	u, err := url.Parse(r.cdpURL)
	if err != nil {
		return r.cdpURL
	}
	host := u.Hostname()
	if host == "" || net.ParseIP(host) != nil || strings.EqualFold(host, "localhost") {
		return r.cdpURL
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(resolveCtx, host)
	if err != nil || len(addrs) == 0 {
		return r.cdpURL
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(addrs[0], port)
	} else {
		u.Host = addrs[0]
	}
	return u.String()
}

// Healthy 探测边车 CDP 端点是否可达(短超时,失败即返回错误)。
func (r *pdfRenderer) Healthy(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, r.resolveCDPURL(probeCtx)+"/json/version", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// RenderPDF 在边车里把自包含 HTML 渲染成 A4 PDF。每次渲染独立 tab,防状态泄漏。
func (r *pdfRenderer) RenderPDF(ctx context.Context, html []byte) ([]byte, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	renderCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(renderCtx, r.resolveCDPURL(renderCtx))
	defer cancelAlloc()
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()

	var pdf []byte
	err := chromedp.Run(tabCtx,
		emulation.SetScriptExecutionDisabled(true),
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			tree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(tree.Frame.ID, string(html)).Do(ctx)
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, _, err := page.PrintToPDF().
				WithPrintBackground(true).
				WithPaperWidth(8.27). // A4 英寸
				WithPaperHeight(11.69).
				WithMarginTop(0.6).
				WithMarginBottom(0.6).
				WithMarginLeft(0.55).
				WithMarginRight(0.55).
				Do(ctx)
			if err != nil {
				return err
			}
			pdf = buf
			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("pdf render failed: %w", err)
	}
	return pdf, nil
}
