package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// webSearchTool 网页搜索工具:经可插拔 searchProvider(首发 Tavily)执行,
// 结果 JSON 喂回模型,引用列表下发前端渲染。provider 错误不终止循环——
// 以 is_error 结果告知模型,让其基于已有知识降级作答。
type webSearchTool struct {
	provider      searchProvider
	maxPerMessage int
	// used 本条消息内已执行的搜索次数(loop 生命周期 = 一次 /chat/completions)。
	used atomic.Int32
}

func (t *webSearchTool) Name() string { return "web_search" }

func (t *webSearchTool) Description() string {
	return "Search the web for up-to-date information. Use this only when the answer depends on recent or time-sensitive facts (news, prices, latest versions, current events) that you may not know reliably. Results include titles, URLs and content excerpts; cite sources by [index] in your answer."
}

func (t *webSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query. Use concise keywords in the language most likely to have good results.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     8,
				"description": "How many results to return (default 5).",
			},
		},
		"required":             []string{"query"},
		"additionalProperties": false,
	}
}

func (t *webSearchTool) Execute(ctx context.Context, tc *toolContext, args json.RawMessage) (*toolOutcome, error) {
	var input struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &input); err != nil || strings.TrimSpace(input.Query) == "" {
		return &toolOutcome{ForModel: "web_search arguments are invalid: a non-empty query string is required", IsError: true}, nil
	}
	if limit := int32(t.maxPerMessage); limit > 0 && t.used.Add(1) > limit {
		return &toolOutcome{
			ForModel: fmt.Sprintf("Search limit for this message reached (%d searches). Answer with the information you already have.", t.maxPerMessage),
			IsError:  true,
		}, nil
	}

	searchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := t.provider.Search(searchCtx, input.Query, searchOptions{MaxResults: input.MaxResults})
	if err != nil {
		tc.logger.Warn("web_search_failed", "provider", t.provider.Name(), "error", err)
		return &toolOutcome{
			ForModel: "web_search is unavailable (" + truncateForModel(err.Error(), 200) + "). Answer from your existing knowledge and state clearly that you could not verify it online.",
			IsError:  true,
		}, nil
	}

	type modelResult struct {
		Index   int    `json:"index"`
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	}
	type clientSource struct {
		Index   int    `json:"index"`
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	}
	forModel := struct {
		Query   string        `json:"query"`
		Results []modelResult `json:"results"`
	}{Query: input.Query, Results: []modelResult{}}
	sources := make([]any, 0, len(resp.Results))
	for i, r := range resp.Results {
		forModel.Results = append(forModel.Results, modelResult{
			Index:   i + 1,
			Title:   r.Title,
			URL:     r.URL,
			Content: truncateForModel(r.Content, 2000),
		})
		sources = append(sources, clientSource{Index: i + 1, Title: r.Title, URL: r.URL, Snippet: r.Snippet})
	}
	encoded, err := json.Marshal(forModel)
	if err != nil {
		return nil, err
	}
	return &toolOutcome{
		ForModel:  truncateForModel(string(encoded), maxToolResultBytes),
		ForClient: map[string]any{"sources": sources},
	}, nil
}
