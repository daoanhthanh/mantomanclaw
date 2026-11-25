package msteams

import (
	"strings"
)

// buildAdaptiveCardTable creates an Adaptive Card Table body element from a
// 2D string slice. The first row is the header row.
func buildAdaptiveCardTable(table [][]string) map[string]any {
	if len(table) == 0 {
		return nil
	}

	columns := make([]map[string]any, 0, len(table[0]))
	for range table[0] {
		columns = append(columns, map[string]any{
			"type":  "Column",
			"width": 1,
		})
	}

	headerCells := make([]map[string]any, 0, len(table[0]))
	for _, cell := range table[0] {
		headerCells = append(headerCells, map[string]any{
			"type":  "TableCell",
			"items": []map[string]any{{"type": "TextBlock", "text": cell, "weight": "bolder"}},
		})
	}

	rows := []map[string]any{
		{
			"type":  "TableRow",
			"style": "accent",
			"cells": headerCells,
		},
	}

	for i := 1; i < len(table); i++ {
		cells := make([]map[string]any, 0, len(table[i]))
		for _, cell := range table[i] {
			cells = append(cells, map[string]any{
				"type":  "TableCell",
				"items": []map[string]any{{"type": "TextBlock", "text": cell}},
			})
		}
		rows = append(rows, map[string]any{
			"type":  "TableRow",
			"cells": cells,
		})
	}

	return map[string]any{
		"type":    "Table",
		"columns": columns,
		"rows":    rows,
	}
}

// markdownToAdaptiveCards splits markdown text into a remaining text portion and
// a slice of Adaptive Card body blocks. It detects GFM-style pipe tables and
// converts them to Adaptive Card Table elements. Non-table text is returned as-is.
func markdownToAdaptiveCards(text string) (string, []map[string]any) {
	lines := strings.Split(text, "\n")
	var (
		textLines  []string
		cardBlocks []map[string]any
	)

	i := 0
	for i < len(lines) {
		line := lines[i]

		// Detect a table: a pipe-delimited line followed by a separator line.
		if strings.Contains(line, "|") && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			tableLines := []string{line}
			i++
			// separator
			i++
			// collect body rows
			for i < len(lines) && strings.Contains(lines[i], "|") {
				tableLines = append(tableLines, lines[i])
				i++
			}
			parsed := parsePipeTable(tableLines)
			if parsed != nil {
				cardBlocks = append(cardBlocks, buildAdaptiveCardTable(parsed))
			}
			continue
		}

		textLines = append(textLines, line)
		i++
	}

	remaining := strings.TrimSpace(strings.Join(textLines, "\n"))
	return remaining, cardBlocks
}

// isTableSeparator returns true if line is a GFM table separator row like "---|---" .
func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.Contains(trimmed, "-") {
		return false
	}
	for _, c := range trimmed {
		if c != '-' && c != '|' && c != ':' && c != ' ' {
			return false
		}
	}
	return true
}

// parsePipeTable parses pipe-delimited rows (including the header and separator)
// into a [][]string. The separator row is skipped.
func parsePipeTable(rows []string) [][]string {
	if len(rows) < 2 {
		return nil
	}
	result := make([][]string, 0, len(rows)-1)
	for idx, row := range rows {
		if idx == 1 {
			continue // skip separator
		}
		fields := splitPipeRow(row)
		if len(fields) == 0 {
			return nil
		}
		result = append(result, fields)
	}
	return result
}

// splitPipeRow splits a pipe-delimited row, trimming whitespace and removing
// leading/trailing pipes.
func splitPipeRow(row string) []string {
	trimmed := strings.TrimSpace(row)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		result = append(result, strings.TrimSpace(p))
	}
	return result
}
