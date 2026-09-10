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

const maxOfficeRendererResponseBytes = 20 << 20

type officeRenderer struct {
	baseURL string
	client  *http.Client
	sem     chan struct{}
}

func newOfficeRenderer(baseURL string) *officeRenderer {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &officeRenderer{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 60 * time.Second},
		sem:     make(chan struct{}, 2),
	}
}

func (r *officeRenderer) Healthy(ctx context.Context) bool {
	if r == nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, r.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (r *officeRenderer) RenderDOCX(ctx context.Context, title, content string) ([]byte, error) {
	return r.render(ctx, "/render/docx", map[string]any{"title": title, "content": content})
}

func (r *officeRenderer) RenderPPTX(ctx context.Context, input presentationInput) ([]byte, error) {
	return r.render(ctx, "/render/pptx", input)
}

func (r *officeRenderer) render(ctx context.Context, path string, payload any) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("office renderer is not configured")
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("office renderer request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxOfficeRendererResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxOfficeRendererResponseBytes {
		return nil, fmt.Errorf("office renderer output exceeds 20MB")
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &apiErr)
		if apiErr.Error == "" {
			apiErr.Error = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("office renderer returned %d: %s", resp.StatusCode, apiErr.Error)
	}
	if len(data) < 4 || !bytes.Equal(data[:2], []byte("PK")) {
		return nil, fmt.Errorf("office renderer returned a file with an invalid signature")
	}
	return data, nil
}
