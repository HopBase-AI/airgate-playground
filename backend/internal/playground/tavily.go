package playground

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ── 搜索提供商抽象(可插拔,首发 Tavily) ────────────────────────────────────────

type searchOptions struct {
	MaxResults int
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
	Content string
}

type searchResponse struct {
	Results []searchResult
}

type searchProvider interface {
	Name() string
	Search(ctx context.Context, query string, opts searchOptions) (*searchResponse, error)
}

// ── Tavily ────────────────────────────────────────────────────────────────────

const tavilyEndpoint = "https://api.tavily.com/search"

type tavilyClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
}

func newTavilyClient(apiKey string) *tavilyClient {
	return &tavilyClient{
		apiKey:     apiKey,
		endpoint:   tavilyEndpoint,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *tavilyClient) Name() string { return "tavily" }

func (c *tavilyClient) Search(ctx context.Context, query string, opts searchOptions) (*searchResponse, error) {
	maxResults := opts.MaxResults
	if maxResults <= 0 || maxResults > 8 {
		maxResults = 5
	}
	payload, err := json.Marshal(map[string]any{
		"query":          query,
		"max_results":    maxResults,
		"search_depth":   "basic",
		"include_answer": false,
	})
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue // 网络错误重试一次
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("tavily upstream %d", resp.StatusCode)
			continue // 5xx 重试一次
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("tavily returned %d: %s", resp.StatusCode, truncateForModel(string(body), 200))
		}
		var parsed struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("tavily response parse failed: %w", err)
		}
		out := &searchResponse{}
		for _, r := range parsed.Results {
			title := strings.TrimSpace(r.Title)
			url := strings.TrimSpace(r.URL)
			if url == "" {
				continue
			}
			out.Results = append(out.Results, searchResult{
				Title:   title,
				URL:     url,
				Snippet: truncateForModel(r.Content, 300),
				Content: r.Content,
			})
		}
		return out, nil
	}
	return nil, lastErr
}
