package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxPresentationArgsBytes = 4 << 20
	maxPresentationSlides    = 15
	maxPresentationBullets   = 8
	maxPresentationTableCols = 8
	maxPresentationTableRows = 20
)

type presentationTable struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

type presentationSlide struct {
	Kind     string             `json:"kind"`
	Title    string             `json:"title"`
	Subtitle string             `json:"subtitle,omitempty"`
	Bullets  []string           `json:"bullets,omitempty"`
	Table    *presentationTable `json:"table,omitempty"`
}

type presentationInput struct {
	Title  string              `json:"title"`
	Slides []presentationSlide `json:"slides"`
}

type generatePresentationTool struct {
	plugin *Plugin
}

func (t *generatePresentationTool) Name() string { return "generate_presentation" }

func (t *generatePresentationTool) Description() string {
	return "Create a downloadable PowerPoint PPTX from structured title, content, and table slides. Keep each slide concise. The file card completes the response, so do not repeat the presentation afterward."
}

func (t *generatePresentationTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string", "description": "Presentation title and file name."},
			"slides": map[string]any{
				"type": "array", "minItems": 1, "maxItems": maxPresentationSlides,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind":     map[string]any{"type": "string", "enum": []string{"title", "content", "table"}},
						"title":    map[string]any{"type": "string", "description": "Slide title, at most 100 characters."},
						"subtitle": map[string]any{"type": "string", "description": "Optional subtitle."},
						"bullets": map[string]any{
							"type": "array", "maxItems": maxPresentationBullets,
							"items": map[string]any{"type": "string", "description": "One concise point, at most 240 characters."},
						},
						"table": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"headers": map[string]any{"type": "array", "minItems": 1, "maxItems": maxPresentationTableCols, "items": map[string]any{"type": "string"}},
								"rows":    map[string]any{"type": "array", "maxItems": maxPresentationTableRows, "items": map[string]any{"type": "array", "maxItems": maxPresentationTableCols, "items": map[string]any{"type": "string"}}},
							},
							"required": []string{"headers", "rows"}, "additionalProperties": false,
						},
					},
					"required": []string{"kind", "title"}, "additionalProperties": false,
				},
			},
		},
		"required": []string{"title", "slides"}, "additionalProperties": false,
	}
}

func (t *generatePresentationTool) Execute(ctx context.Context, tc *toolContext, args json.RawMessage) (*toolOutcome, error) {
	if len(args) > maxPresentationArgsBytes {
		return &toolOutcome{ForModel: "Presentation arguments exceed the 4MB limit. Reduce the content.", IsError: true}, nil
	}
	var input presentationInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &toolOutcome{ForModel: "generate_presentation arguments are not a valid presentation structure", IsError: true}, nil
	}
	if tc.conversationID <= 0 {
		return &toolOutcome{ForModel: "This request has no conversation context, so the presentation cannot be saved.", IsError: true}, nil
	}
	if err := validatePresentation(input); err != nil {
		return &toolOutcome{ForModel: "Invalid presentation structure: " + err.Error(), IsError: true}, nil
	}
	renderer := t.plugin.office
	if renderer == nil || !renderer.Healthy(ctx) {
		return &toolOutcome{ForModel: "Office renderer is unreachable, PPTX cannot be generated right now.", IsError: true}, nil
	}
	data, err := renderer.RenderPPTX(ctx, input)
	if err != nil {
		tc.logger.Warn("generate_presentation_render_failed", "error", err)
		return &toolOutcome{ForModel: "PPTX generation failed: " + err.Error(), IsError: true}, nil
	}
	storage := t.plugin.svc.Storage()
	if storage == nil {
		return &toolOutcome{ForModel: "Document storage is unavailable.", IsError: true}, nil
	}
	asset, err := storage.StoreDocumentBytes(ctx, int(tc.userID), "application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx", data)
	if err != nil {
		return &toolOutcome{ForModel: "Saving the PPTX failed. Please retry later.", IsError: true}, nil
	}
	if err := t.plugin.svc.RegisterConversationAsset(ctx, int(tc.userID), tc.conversationID, asset); err != nil {
		tc.logger.Warn("generate_presentation_register_failed", "error", err)
		_ = storage.Delete(ctx, asset.ObjectKey)
		return &toolOutcome{ForModel: "The PPTX was generated but registering it as a conversation asset failed. Please retry later.", IsError: true}, nil
	}
	// First rollout uses a fixed per-file fee; slide count is a quality/usage
	// metric in the future, not an extra multiplier on the user's charge.
	usage, err := t.plugin.chargeRenderUsage(ctx, tc, "pptx", asset.ID, asset.SizeBytes, 1)
	if err != nil {
		_ = t.plugin.svc.RemoveConversationAsset(ctx, int(tc.userID), tc.conversationID, asset)
		return &toolOutcome{ForModel: "The PPTX was rendered but billing the file fee failed: " + err.Error(), IsError: true}, nil
	}
	title := sanitizeDocumentTitle(input.Title)
	return &toolOutcome{
		ForModel: fmt.Sprintf("Generated presentation %q (PPTX, %dKB) and delivered it to the user.", title, asset.SizeBytes>>10),
		ForClient: map[string]any{"file": map[string]any{
			"name": title + ".pptx", "content_type": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			"size": asset.SizeBytes, "src": asset.PublicURL, "asset_uri": assetURI(asset.ID),
		}},
		Terminal: true, TerminalMessage: "PowerPoint file generated. You can download it from the file card.", Usage: usage,
	}, nil
}

func validatePresentation(input presentationInput) error {
	if strings.TrimSpace(input.Title) == "" || utf8.RuneCountInString(input.Title) > 120 {
		return fmt.Errorf("title must be non-empty and at most 120 characters")
	}
	if len(input.Slides) == 0 || len(input.Slides) > maxPresentationSlides {
		return fmt.Errorf("slide count must be between 1 and %d", maxPresentationSlides)
	}
	for index, slide := range input.Slides {
		if strings.TrimSpace(slide.Title) == "" || utf8.RuneCountInString(slide.Title) > 100 {
			return fmt.Errorf("slide %d: title must be non-empty and at most 100 characters", index+1)
		}
		if utf8.RuneCountInString(slide.Subtitle) > 300 || len(slide.Bullets) > maxPresentationBullets {
			return fmt.Errorf("slide %d: content exceeds the limit", index+1)
		}
		for _, bullet := range slide.Bullets {
			if utf8.RuneCountInString(bullet) > 240 {
				return fmt.Errorf("slide %d: bullet exceeds 240 characters", index+1)
			}
		}
		switch slide.Kind {
		case "title", "content":
		case "table":
			if slide.Table == nil || len(slide.Table.Headers) == 0 || len(slide.Table.Headers) > maxPresentationTableCols || len(slide.Table.Rows) > maxPresentationTableRows {
				return fmt.Errorf("slide %d: invalid table structure", index+1)
			}
			for _, row := range slide.Table.Rows {
				if len(row) > len(slide.Table.Headers) {
					return fmt.Errorf("slide %d: table row has more cells than columns", index+1)
				}
				for _, cell := range row {
					if utf8.RuneCountInString(cell) > 200 {
						return fmt.Errorf("slide %d: table cell exceeds 200 characters", index+1)
					}
				}
			}
		default:
			return fmt.Errorf("slide %d: unsupported kind", index+1)
		}
	}
	return nil
}
