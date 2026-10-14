package blog

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// bodyPolicy limits post bodies to the markup the blog editor produces, so
// published HTML can be rendered as-is on the public site.
var bodyPolicy = newBodyPolicy()

func newBodyPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"p", "br", "h2", "h3", "strong", "b", "em", "i", "u", "s",
		"code", "pre", "blockquote", "ul", "ol", "li", "hr", "mark",
	)
	policy.AllowStyles("text-align").
		MatchingEnum("left", "center", "right", "justify").
		OnElements("p", "h2", "h3")
	policy.AllowAttrs("start").Matching(regexp.MustCompile(`^\d{1,4}$`)).OnElements("ol")

	policy.AllowAttrs("href").OnElements("a")
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.AllowRelativeURLs(true)
	policy.RequireNoFollowOnLinks(true)
	policy.AddTargetBlankToFullyQualifiedLinks(true)

	policy.AllowAttrs("src").
		Matching(regexp.MustCompile(`^(/api/v1/blog/media/[0-9a-f-]{36}|https://[^\s"'<>]+)$`)).
		OnElements("img")
	policy.AllowAttrs("alt", "title").OnElements("img")
	return policy
}

func sanitizeBody(html string) string {
	return bodyPolicy.Sanitize(html)
}
