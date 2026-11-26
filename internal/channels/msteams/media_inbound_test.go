package msteams

import (
	"reflect"
	"testing"
)

func TestSiteAllowedMatchesCompositeAndParts(t *testing.T) {
	g := newGraphClient(nil, []string{"site-coll-guid"}, nil)
	cases := map[string]bool{
		"":               false,
		"site-coll-guid": true,
		"contoso.sharepoint.com,site-coll-guid,web-guid": true,
		"contoso.sharepoint.com,other,web-guid":          false,
	}
	for id, want := range cases {
		if got := g.siteAllowed(id); got != want {
			t.Errorf("siteAllowed(%q)=%v want %v", id, got, want)
		}
	}

	full := newGraphClient(nil, []string{"contoso.sharepoint.com,a,b"}, nil)
	if !full.siteAllowed("contoso.sharepoint.com,a,b") {
		t.Error("full composite id should match")
	}
}

func TestExtractSharePointLinksFromHTMLHref(t *testing.T) {
	text := `<p>see <a href="https://contoso.sharepoint.com/sites/x/Shared%20Documents/a.docx">doc</a> and https://contoso.sharepoint.com/sites/x/b.pdf.</p>`
	want := []string{
		"https://contoso.sharepoint.com/sites/x/Shared%20Documents/a.docx",
		"https://contoso.sharepoint.com/sites/x/b.pdf",
	}
	if got := extractSharePointLinks(text); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
