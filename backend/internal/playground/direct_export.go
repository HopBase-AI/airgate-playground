package playground

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	goldmarkast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Direct export intentionally parses the persisted Markdown AST instead of
// splitting table rows with ad-hoc string operations. It is a zero-model-token
// path: the source message is already owned by the user and only gets rendered
// into the requested file format.

var directExportMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func markdownAST(content string) gast.Node {
	return directExportMarkdown.Parser().Parse(text.NewReader([]byte(content)))
}

func markdownNodeText(node gast.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return strings.TrimSpace(string(node.Text(source)))
}

func markdownTableCells(node gast.Node, source []byte) []string {
	values := make([]string, 0, 8)
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if _, ok := child.(*goldmarkast.TableCell); !ok {
			continue
		}
		values = append(values, markdownNodeText(child, source))
	}
	return values
}

type directMarkdownTable struct {
	Headers []string
	Rows    [][]string
}

func markdownTableFromNode(table *goldmarkast.Table, source []byte) directMarkdownTable {
	var result directMarkdownTable
	for child := table.FirstChild(); child != nil; child = child.NextSibling() {
		switch row := child.(type) {
		case *goldmarkast.TableHeader:
			result.Headers = markdownTableCells(row, source)
		case *goldmarkast.TableRow:
			result.Rows = append(result.Rows, markdownTableCells(row, source))
		}
	}
	return result
}

func markdownTables(content string) []directMarkdownTable {
	source := []byte(content)
	root := markdownAST(content)
	var tables []directMarkdownTable
	_ = gast.Walk(root, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		if table, ok := node.(*goldmarkast.Table); ok {
			result := markdownTableFromNode(table, source)
			if len(result.Headers) > 0 {
				tables = append(tables, result)
			}
			return gast.WalkSkipChildren, nil
		}
		return gast.WalkContinue, nil
	})
	return tables
}

func markdownTextBlocks(content string) []string {
	source := []byte(content)
	root := markdownAST(content)
	var blocks []string
	_ = gast.Walk(root, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		switch node.(type) {
		case *gast.Heading, *gast.Paragraph, *gast.TextBlock, *gast.FencedCodeBlock, *gast.CodeBlock:
			if value := markdownNodeText(node, source); value != "" {
				blocks = append(blocks, value)
			}
			return gast.WalkSkipChildren, nil
		case *goldmarkast.Table:
			return gast.WalkSkipChildren, nil
		}
		return gast.WalkContinue, nil
	})
	return blocks
}

func canonicalInteger(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") {
		return false
	}
	digits := strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	if len(digits) > 1 && strings.HasPrefix(digits, "0") {
		return false
	}
	if len(digits) > 15 {
		return false // Excel numeric cells only preserve about 15 significant digits.
	}
	_, err := strconv.ParseInt(value, 10, 64)
	return err == nil
}

func canonicalNumber(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") || canonicalInteger(value) {
		return false
	}
	unsigned := strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	integerPart := unsigned
	if cut := strings.IndexAny(integerPart, ".eE"); cut >= 0 {
		integerPart = integerPart[:cut]
	}
	if len(integerPart) > 1 && strings.HasPrefix(integerPart, "0") {
		return false
	}
	digitCount := 0
	for _, char := range unsigned {
		if char >= '0' && char <= '9' {
			digitCount++
		}
	}
	if digitCount == 0 || digitCount > 15 {
		return false
	}
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsInf(number, 0) && !math.IsNaN(number)
}

func inferSpreadsheetColumn(values []string) (columnType, format string) {
	nonEmpty := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			nonEmpty = append(nonEmpty, value)
		}
	}
	if len(nonEmpty) == 0 {
		return "string", ""
	}
	all := func(predicate func(string) bool) bool {
		for _, value := range nonEmpty {
			if !predicate(value) {
				return false
			}
		}
		return true
	}
	if all(canonicalInteger) {
		return "integer", "integer"
	}
	if all(func(value string) bool { return canonicalInteger(value) || canonicalNumber(value) }) {
		return "number", "decimal"
	}
	if all(func(value string) bool {
		_, err := time.Parse("2006-01-02", strings.TrimSpace(value))
		return err == nil
	}) {
		return "date", "date"
	}
	if all(func(value string) bool {
		_, err := strconv.ParseBool(strings.TrimSpace(value))
		return err == nil
	}) {
		return "boolean", "general"
	}
	return "string", ""
}

func markdownToSpreadsheet(title, content string) (spreadsheetInput, error) {
	if strings.TrimSpace(content) == "" {
		return spreadsheetInput{}, fmt.Errorf("message content is empty, nothing to export to Excel")
	}
	tables := markdownTables(content)
	if len(tables) == 0 {
		blocks := markdownTextBlocks(content)
		rows := make([][]any, 0, len(blocks))
		for _, block := range blocks {
			rows = append(rows, []any{block})
		}
		if len(rows) == 0 {
			rows = append(rows, []any{strings.TrimSpace(content)})
		}
		return spreadsheetInput{Title: title, Sheets: []spreadsheetSheet{{
			Name: "Content", Columns: []spreadsheetColumn{{Header: "Content", Type: "string", Width: 60}}, Rows: rows,
		}}}, nil
	}
	sheets := make([]spreadsheetSheet, 0, len(tables))
	for i, table := range tables {
		if len(table.Headers) > maxSpreadsheetColumns {
			return spreadsheetInput{}, fmt.Errorf("table %d has more than %d columns", i+1, maxSpreadsheetColumns)
		}
		columns := make([]spreadsheetColumn, len(table.Headers))
		for j, header := range table.Headers {
			if strings.TrimSpace(header) == "" {
				header = fmt.Sprintf("Column %d", j+1)
			}
			values := make([]string, 0, len(table.Rows))
			for _, row := range table.Rows {
				if j < len(row) {
					values = append(values, row[j])
				}
			}
			columnType, format := inferSpreadsheetColumn(values)
			columns[j] = spreadsheetColumn{Header: header, Type: columnType, Format: format}
		}
		rows := make([][]any, 0, len(table.Rows))
		for _, row := range table.Rows {
			cells := make([]any, len(columns))
			for j := range cells {
				if j < len(row) {
					cells[j] = row[j]
				}
			}
			rows = append(rows, cells)
		}
		sheets = append(sheets, spreadsheetSheet{
			Name: fmt.Sprintf("Sheet %d", i+1), Columns: columns, Rows: rows,
		})
	}
	return spreadsheetInput{Title: title, Sheets: sheets}, nil
}

func splitPresentationText(value string, limit int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if limit <= 0 {
		limit = 240
	}
	runes := []rune(value)
	var parts []string
	for len(runes) > limit {
		cut := limit
		for i := limit; i > limit/2; i-- {
			if strings.ContainsRune("。！？；，、.!?;,: ", runes[i-1]) {
				cut = i
				break
			}
		}
		part := strings.TrimSpace(string(runes[:cut]))
		if part != "" {
			parts = append(parts, part)
		}
		runes = runes[cut:]
	}
	if tail := strings.TrimSpace(string(runes)); tail != "" {
		parts = append(parts, tail)
	}
	return parts
}

func markdownToPresentation(title, content string) (presentationInput, error) {
	if strings.TrimSpace(content) == "" {
		return presentationInput{}, fmt.Errorf("message content is empty, nothing to export to PowerPoint")
	}
	source := []byte(content)
	root := markdownAST(content)
	slides := []presentationSlide{{Kind: "title", Title: title}}
	currentTitle := "Highlights"
	currentBullets := make([]string, 0, maxPresentationBullets)
	flush := func() {
		if len(currentBullets) == 0 {
			return
		}
		slides = append(slides, presentationSlide{Kind: "content", Title: currentTitle, Bullets: append([]string(nil), currentBullets...)})
		currentBullets = currentBullets[:0]
	}
	addText := func(value string) {
		for _, part := range splitPresentationText(value, 240) {
			if len(currentBullets) >= maxPresentationBullets {
				flush()
			}
			currentBullets = append(currentBullets, part)
		}
	}
	var addNode func(gast.Node) error
	addNode = func(node gast.Node) error {
		switch block := node.(type) {
		case *gast.Heading:
			heading := markdownNodeText(block, source)
			if heading == "" || strings.EqualFold(heading, title) {
				return nil
			}
			flush()
			parts := splitPresentationText(heading, 100)
			if len(parts) == 0 {
				return nil
			}
			currentTitle = parts[0]
			for _, part := range parts[1:] {
				addText(part)
			}
			return nil
		case *gast.Paragraph, *gast.TextBlock, *gast.FencedCodeBlock, *gast.CodeBlock:
			addText(markdownNodeText(block, source))
			return nil
		case *goldmarkast.Table:
			flush()
			table := markdownTableFromNode(block, source)
			if len(table.Headers) == 0 {
				return fmt.Errorf("PowerPoint table has no header row")
			}
			validTable := len(table.Headers) <= maxPresentationTableCols
			for _, cell := range append(append([]string(nil), table.Headers...), flattenTableRows(table.Rows)...) {
				if utf8.RuneCountInString(cell) > 200 {
					validTable = false
					break
				}
			}
			if !validTable {
				addText(strings.Join(table.Headers, " | "))
				for _, row := range table.Rows {
					addText(strings.Join(row, " | "))
				}
				return nil
			}
			parts := max(1, (len(table.Rows)+maxPresentationTableRows-1)/maxPresentationTableRows)
			if len(table.Rows) == 0 {
				slides = append(slides, presentationSlide{Kind: "table", Title: currentTitle, Table: &presentationTable{Headers: table.Headers}})
				return nil
			}
			for start := 0; start < len(table.Rows); start += maxPresentationTableRows {
				end := min(start+maxPresentationTableRows, len(table.Rows))
				subtitle := ""
				if parts > 1 {
					subtitle = fmt.Sprintf("Part %d/%d", start/maxPresentationTableRows+1, parts)
				}
				slides = append(slides, presentationSlide{
					Kind: "table", Title: currentTitle, Subtitle: subtitle,
					Table: &presentationTable{Headers: append([]string(nil), table.Headers...), Rows: append([][]string(nil), table.Rows[start:end]...)},
				})
			}
			return nil
		default:
			for child := node.FirstChild(); child != nil; child = child.NextSibling() {
				if err := addNode(child); err != nil {
					return err
				}
			}
			return nil
		}
	}
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		if err := addNode(node); err != nil {
			return presentationInput{}, err
		}
	}
	flush()
	if len(slides) > maxPresentationSlides {
		return presentationInput{}, fmt.Errorf("content needs %d slides, over the %d slide limit", len(slides), maxPresentationSlides)
	}
	return presentationInput{Title: title, Slides: slides}, nil
}

func flattenTableRows(rows [][]string) []string {
	var out []string
	for _, row := range rows {
		out = append(out, row...)
	}
	return out
}
