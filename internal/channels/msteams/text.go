package msteams

import (
	"html"
	"regexp"
	"strings"
)

var (
	rePTag  = regexp.MustCompile(`(?i)</?p\b[^>]*>`)
	reBrTag = regexp.MustCompile(`(?i)<br\s*/?>`)
	reTag   = regexp.MustCompile(`<[^>]+>`)
)

// normalizeHTML converts Teams HTML text to plain markdown-ish text:
// - <p>/<br> become newlines
// - HTML entities are decoded
// - all other tags are stripped
func normalizeHTML(s string) string {
	s = rePTag.ReplaceAllString(s, "\n")
	s = reBrTag.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}
