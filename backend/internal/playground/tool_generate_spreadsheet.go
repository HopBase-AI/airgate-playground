package playground

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

const (
	maxSpreadsheetArgsBytes    = 4 << 20
	maxSpreadsheetBytes        = 20 << 20
	maxSpreadsheetSheets       = 10
	maxSpreadsheetColumns      = 40
	maxSpreadsheetRows         = 5000
	maxSpreadsheetCellRunes    = 10000
	defaultSpreadsheetColWidth = 14
)

type spreadsheetColumn struct {
	Header string  `json:"header"`
	Type   string  `json:"type,omitempty"`
	Format string  `json:"format,omitempty"`
	Width  float64 `json:"width,omitempty"`
}

type spreadsheetSheet struct {
	Name         string              `json:"name"`
	Columns      []spreadsheetColumn `json:"columns"`
	Rows         [][]any             `json:"rows"`
	FreezeHeader *bool               `json:"freeze_header,omitempty"`
}

type spreadsheetInput struct {
	Title  string             `json:"title"`
	Sheets []spreadsheetSheet `json:"sheets"`
}

type generateSpreadsheetTool struct {
	plugin *Plugin
}

func (t *generateSpreadsheetTool) Name() string { return "generate_spreadsheet" }

func (t *generateSpreadsheetTool) Description() string {
	return "Create a downloadable XLSX workbook from structured sheets, columns, and rows. Use it for tables, budgets, inventories, schedules, or analysis data. The file card completes the response, so do not repeat the workbook contents afterward."
}

func (t *generateSpreadsheetTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string", "description": "Workbook title and file name."},
			"sheets": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": maxSpreadsheetSheets,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string", "description": "Unique Excel sheet name, at most 31 characters."},
						"columns": map[string]any{
							"type": "array", "minItems": 1, "maxItems": maxSpreadsheetColumns,
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"header": map[string]any{"type": "string"},
									"type":   map[string]any{"type": "string", "enum": []string{"string", "number", "integer", "boolean", "date", "datetime"}},
									"format": map[string]any{"type": "string", "enum": []string{"general", "integer", "decimal", "currency", "percent", "date", "datetime"}},
									"width":  map[string]any{"type": "number", "minimum": 6, "maximum": 60},
								},
								"required": []string{"header"}, "additionalProperties": false,
							},
						},
						"rows": map[string]any{
							"type": "array", "maxItems": maxSpreadsheetRows,
							"items": map[string]any{"type": "array", "maxItems": maxSpreadsheetColumns},
						},
						"freeze_header": map[string]any{"type": "boolean"},
					},
					"required": []string{"name", "columns", "rows"}, "additionalProperties": false,
				},
			},
		},
		"required": []string{"title", "sheets"}, "additionalProperties": false,
	}
}

func (t *generateSpreadsheetTool) Execute(ctx context.Context, tc *toolContext, args json.RawMessage) (*toolOutcome, error) {
	if len(args) > maxSpreadsheetArgsBytes {
		return &toolOutcome{ForModel: "Spreadsheet arguments exceed the 4MB limit. Reduce the amount of data.", IsError: true}, nil
	}
	var input spreadsheetInput
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return &toolOutcome{ForModel: "generate_spreadsheet arguments are not a valid workbook structure", IsError: true}, nil
	}
	if tc.conversationID <= 0 {
		return &toolOutcome{ForModel: "This request has no conversation context, so the spreadsheet cannot be saved.", IsError: true}, nil
	}
	data, err := renderSpreadsheet(input)
	if err != nil {
		return &toolOutcome{ForModel: "Spreadsheet generation failed: " + err.Error(), IsError: true}, nil
	}
	if len(data) > maxSpreadsheetBytes {
		return &toolOutcome{ForModel: "The generated XLSX exceeds the 20MB limit. Reduce the amount of data.", IsError: true}, nil
	}
	storage := t.plugin.svc.Storage()
	if storage == nil {
		return &toolOutcome{ForModel: "Document storage is unavailable.", IsError: true}, nil
	}
	asset, err := storage.StoreDocumentBytes(ctx, int(tc.userID), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx", data)
	if err != nil {
		tc.logger.Warn("generate_spreadsheet_store_failed", "error", err)
		return &toolOutcome{ForModel: "Saving the XLSX failed. Please retry later.", IsError: true}, nil
	}
	if err := t.plugin.svc.RegisterConversationAsset(ctx, int(tc.userID), tc.conversationID, asset); err != nil {
		tc.logger.Warn("generate_spreadsheet_register_failed", "error", err)
		_ = storage.Delete(ctx, asset.ObjectKey)
		return &toolOutcome{ForModel: "The XLSX was generated but registering it as a conversation asset failed. Please retry later.", IsError: true}, nil
	}
	usage, err := t.plugin.chargeRenderUsage(ctx, tc, "xlsx", asset.ID, asset.SizeBytes, 1)
	if err != nil {
		_ = t.plugin.svc.RemoveConversationAsset(ctx, int(tc.userID), tc.conversationID, asset)
		return &toolOutcome{ForModel: "The XLSX was rendered but billing the file fee failed: " + err.Error(), IsError: true}, nil
	}
	title := sanitizeDocumentTitle(input.Title)
	return &toolOutcome{
		ForModel: fmt.Sprintf("Generated Excel workbook %q (XLSX, %dKB) and delivered it to the user.", title, asset.SizeBytes>>10),
		ForClient: map[string]any{"file": map[string]any{
			"name": title + ".xlsx", "content_type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"size": asset.SizeBytes, "src": asset.PublicURL, "asset_uri": assetURI(asset.ID),
		}},
		Terminal: true, TerminalMessage: "Excel file generated. You can download it from the file card.", Usage: usage,
	}, nil
}

func renderSpreadsheet(input spreadsheetInput) ([]byte, error) {
	if strings.TrimSpace(input.Title) == "" {
		return nil, fmt.Errorf("title must not be empty")
	}
	if len(input.Sheets) == 0 || len(input.Sheets) > maxSpreadsheetSheets {
		return nil, fmt.Errorf("sheet count must be between 1 and %d", maxSpreadsheetSheets)
	}
	seen := make(map[string]struct{}, len(input.Sheets))
	totalRows := 0
	for i := range input.Sheets {
		if err := validateSpreadsheetSheet(input.Sheets[i], seen); err != nil {
			return nil, err
		}
		totalRows += len(input.Sheets[i].Rows)
	}
	if totalRows > maxSpreadsheetRows {
		return nil, fmt.Errorf("all sheets combined may contain at most %d rows", maxSpreadsheetRows)
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Family: "Arial", Size: 11},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"2563EB"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Border:    []excelize.Border{{Type: "bottom", Color: "D1D5DB", Style: 1}},
	})
	if err != nil {
		return nil, err
	}
	styles := make(map[string]int)
	for sheetIndex, sheet := range input.Sheets {
		name := strings.TrimSpace(sheet.Name)
		if sheetIndex == 0 {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				return nil, err
			}
		} else if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
		if err := writeSpreadsheetSheet(f, name, sheet, headerStyle, styles); err != nil {
			return nil, err
		}
	}
	f.SetActiveSheet(0)
	buffer, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func validateSpreadsheetSheet(sheet spreadsheetSheet, seen map[string]struct{}) error {
	name := strings.TrimSpace(sheet.Name)
	if name == "" || utf8.RuneCountInString(name) > 31 || strings.ContainsAny(name, `[]:*?/\\`) {
		return fmt.Errorf("invalid sheet name: %q", sheet.Name)
	}
	key := strings.ToLower(name)
	if _, ok := seen[key]; ok {
		return fmt.Errorf("duplicate sheet name: %q", name)
	}
	seen[key] = struct{}{}
	if len(sheet.Columns) == 0 || len(sheet.Columns) > maxSpreadsheetColumns {
		return fmt.Errorf("sheet %q must have between 1 and %d columns", name, maxSpreadsheetColumns)
	}
	for _, column := range sheet.Columns {
		if strings.TrimSpace(column.Header) == "" {
			return fmt.Errorf("sheet %q has an empty header", name)
		}
		if column.Width != 0 && (column.Width < 6 || column.Width > 60) {
			return fmt.Errorf("sheet %q column width must be between 6 and 60", name)
		}
		if !validSpreadsheetType(column.Type) || !validSpreadsheetFormat(column.Format) {
			return fmt.Errorf("sheet %q contains an unsupported column type or format", name)
		}
	}
	for rowIndex, row := range sheet.Rows {
		if len(row) > len(sheet.Columns) {
			return fmt.Errorf("sheet %q row %d has more cells than columns", name, rowIndex+1)
		}
	}
	return nil
}

func validSpreadsheetType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "string", "number", "integer", "boolean", "date", "datetime":
		return true
	default:
		return false
	}
}

func validSpreadsheetFormat(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "general", "integer", "decimal", "currency", "percent", "date", "datetime":
		return true
	default:
		return false
	}
}

func writeSpreadsheetSheet(f *excelize.File, name string, sheet spreadsheetSheet, headerStyle int, styles map[string]int) error {
	showGridLines := false
	if err := f.SetSheetView(name, 0, &excelize.ViewOptions{ShowGridLines: &showGridLines}); err != nil {
		return err
	}
	freeze := sheet.FreezeHeader == nil || *sheet.FreezeHeader
	if freeze {
		if err := f.SetPanes(name, &excelize.Panes{Freeze: true, Split: false, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
			return err
		}
	}
	if err := f.SetRowHeight(name, 1, 24); err != nil {
		return err
	}
	columnWidths := make([]float64, len(sheet.Columns))
	for columnIndex, column := range sheet.Columns {
		cell, _ := excelize.CoordinatesToCellName(columnIndex+1, 1)
		if err := f.SetCellStr(name, cell, column.Header); err != nil {
			return err
		}
		if err := f.SetCellStyle(name, cell, cell, headerStyle); err != nil {
			return err
		}
		columnName, _ := excelize.ColumnNumberToName(columnIndex + 1)
		width := column.Width
		if width == 0 {
			width = inferredSpreadsheetWidth(column, sheet.Rows, columnIndex)
		}
		columnWidths[columnIndex] = width
		if err := f.SetColWidth(name, columnName, columnName, width); err != nil {
			return err
		}
	}
	lastColumn, _ := excelize.ColumnNumberToName(len(sheet.Columns))
	if err := f.AutoFilter(name, fmt.Sprintf("A1:%s%d", lastColumn, len(sheet.Rows)+1), nil); err != nil {
		return err
	}
	for rowIndex, row := range sheet.Rows {
		for columnIndex, value := range row {
			cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+2)
			typed, err := spreadsheetCellValue(value, sheet.Columns[columnIndex])
			if err != nil {
				return fmt.Errorf("sheet %q row %d column %d: %w", name, rowIndex+1, columnIndex+1, err)
			}
			if err := setSpreadsheetCellValue(f, name, cell, typed); err != nil {
				return err
			}
			styleID, err := spreadsheetCellStyle(f, sheet.Columns[columnIndex], styles)
			if err != nil {
				return err
			}
			if styleID > 0 {
				if err := f.SetCellStyle(name, cell, cell, styleID); err != nil {
					return err
				}
			}
		}
		if height := inferredSpreadsheetRowHeight(row, columnWidths); height > 20 {
			if err := f.SetRowHeight(name, rowIndex+2, height); err != nil {
				return err
			}
		}
	}
	return nil
}

func inferredSpreadsheetRowHeight(row []any, columnWidths []float64) float64 {
	maxLines := 1
	for index, value := range row {
		if index >= len(columnWidths) || value == nil {
			continue
		}
		charsPerLine := int(math.Max(1, columnWidths[index]-2))
		lines := 0
		for _, segment := range strings.Split(fmt.Sprint(value), "\n") {
			count := utf8.RuneCountInString(segment)
			lines += max(1, (count+charsPerLine-1)/charsPerLine)
		}
		if lines > maxLines {
			maxLines = lines
		}
	}
	height := float64(maxLines*18 + 4)
	if height > 240 {
		return 240
	}
	return height
}

func spreadsheetCellValue(value any, column spreadsheetColumn) (any, error) {
	if value == nil {
		return "", nil
	}
	kind := strings.ToLower(strings.TrimSpace(column.Type))
	switch kind {
	case "number", "integer":
		n, err := spreadsheetNumber(value)
		if err != nil {
			return nil, err
		}
		if kind == "integer" {
			return int64(math.Round(n)), nil
		}
		return n, nil
	case "boolean":
		switch v := value.(type) {
		case bool:
			return v, nil
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("cannot parse boolean %q", v)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("boolean column requires true/false")
		}
	case "date", "datetime":
		text := fmt.Sprint(value)
		layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, text); err == nil {
				return parsed, nil
			}
		}
		return nil, fmt.Errorf("date must use YYYY-MM-DD, YYYY-MM-DD HH:MM:SS or RFC3339")
	default:
		text := fmt.Sprint(value)
		if utf8.RuneCountInString(text) > maxSpreadsheetCellRunes {
			return nil, fmt.Errorf("text exceeds %d characters", maxSpreadsheetCellRunes)
		}
		return text, nil
	}
}

func spreadsheetNumber(value any) (float64, error) {
	switch v := value.(type) {
	case json.Number:
		return v.Float64()
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	default:
		return 0, fmt.Errorf("number column contains a non-numeric value")
	}
}

func setSpreadsheetCellValue(f *excelize.File, sheet, cell string, value any) error {
	if text, ok := value.(string); ok {
		// SetCellStr 明确写入 shared string，不让 =、+、-、@ 开头的用户文本变成公式。
		return f.SetCellStr(sheet, cell, text)
	}
	return f.SetCellValue(sheet, cell, value)
}

func spreadsheetCellStyle(f *excelize.File, column spreadsheetColumn, cache map[string]int) (int, error) {
	format := strings.ToLower(strings.TrimSpace(column.Format))
	if format == "" {
		format = strings.ToLower(strings.TrimSpace(column.Type))
	}
	if format == "" {
		format = "general"
	}
	cacheKey := format + ":" + strings.ToLower(strings.TrimSpace(column.Type))
	if style, ok := cache[cacheKey]; ok {
		return style, nil
	}
	style := &excelize.Style{
		Font:      &excelize.Font{Family: "Arial", Size: 10, Color: "1E293B"},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Border:    []excelize.Border{{Type: "bottom", Color: "E2E8F0", Style: 1}},
	}
	if strings.EqualFold(column.Type, "number") || strings.EqualFold(column.Type, "integer") || strings.EqualFold(column.Type, "date") || strings.EqualFold(column.Type, "datetime") {
		style.Alignment.Horizontal = "right"
	}
	switch format {
	case "integer":
		style.NumFmt = 1
	case "number", "decimal":
		style.NumFmt = 2
	case "currency":
		custom := `#,##0.00;[Red]-#,##0.00`
		style.CustomNumFmt = &custom
	case "percent":
		style.NumFmt = 10
	case "date":
		custom := "yyyy-mm-dd"
		style.CustomNumFmt = &custom
	case "datetime":
		custom := "yyyy-mm-dd hh:mm:ss"
		style.CustomNumFmt = &custom
	default:
		style.NumFmt = 0
	}
	styleID, err := f.NewStyle(style)
	if err != nil {
		return 0, err
	}
	cache[cacheKey] = styleID
	return styleID, nil
}

func inferredSpreadsheetWidth(column spreadsheetColumn, rows [][]any, index int) float64 {
	width := utf8.RuneCountInString(column.Header) + 2
	limit := len(rows)
	if limit > 100 {
		limit = 100
	}
	for _, row := range rows[:limit] {
		if index >= len(row) {
			continue
		}
		length := utf8.RuneCountInString(fmt.Sprint(row[index])) + 2
		if length > width {
			width = length
		}
	}
	if width < defaultSpreadsheetColWidth {
		width = defaultSpreadsheetColWidth
	}
	if width > 60 {
		width = 60
	}
	return float64(width)
}
