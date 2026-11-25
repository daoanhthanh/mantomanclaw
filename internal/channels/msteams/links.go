package msteams

import (
	"regexp"
	"strings"
)

// reSPLink matches SharePoint/OneDrive URLs in message text.
// Only matches https://<tenant>.sharepoint.com/... to avoid false positives.
var reSPLink = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.sharepoint\.com/[^\s<>"']+`)

const maxLinksPerMsg = 5

// extractSharePointLinks extracts up to maxLinksPerMsg unique SharePoint URLs from text.
func extractSharePointLinks(text string) []string {
	matches := reSPLink.FindAllString(text, -1)
	seen := make(map[string]bool)
	var out []string
	for _, m := range matches {
		// Strip trailing punctuation.
		m = strings.TrimRight(m, ".,;:!?)")
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
		if len(out) >= maxLinksPerMsg {
			break
		}
	}
	return out
}
