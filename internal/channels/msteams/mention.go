package msteams

import (
	"regexp"
	"strings"
)

// aadMentionRe matches @<aadObjectId> tokens (bare) or <at id="...">...</at> XML-style mentions.
var aadMentionRe = regexp.MustCompile(`(?:<at id="([^"]+)">[^<]*</at>|@([0-9a-fA-F-]{36}))`)

// extractMentions strips mention tokens from text and returns the cleaned text
// plus the list of AAD object IDs mentioned.
func extractMentions(text string) (stripped string, aadIDs []string) {
	seen := map[string]bool{}
	result := aadMentionRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := aadMentionRe.FindStringSubmatch(match)
		id := sub[1]
		if id == "" {
			id = sub[2]
		}
		if id != "" && !seen[id] {
			seen[id] = true
			aadIDs = append(aadIDs, id)
		}
		return ""
	})
	stripped = strings.TrimSpace(result)
	return stripped, aadIDs
}

// buildMentionEntities converts a list of AAD object IDs into Bot Framework
// mention entity objects suitable for inclusion in an activity's entities array.
func buildMentionEntities(mentions []string) []map[string]any {
	if len(mentions) == 0 {
		return nil
	}
	entities := make([]map[string]any, 0, len(mentions))
	for _, id := range mentions {
		entities = append(entities, map[string]any{
			"type": "mention",
			"mentioned": map[string]any{
				"id":   id,
				"name": id,
			},
			"text": "<at>" + id + "</at>",
		})
	}
	return entities
}
