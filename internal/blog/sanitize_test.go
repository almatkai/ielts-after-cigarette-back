package blog

import (
	"strings"
	"testing"
)

func TestSanitizeBodyKeepsEditorMarkup(t *testing.T) {
	input := `<h2 style="text-align: center">Plan</h2><p><strong>b</strong> <em>i</em> <u>u</u> <s>s</s> <mark>m</mark> <code>c</code></p>` +
		`<ul><li><p>one</p></li></ul><ol start="3"><li><p>two</p></li></ol><blockquote><p>q</p></blockquote>` +
		`<pre><code>x</code></pre><hr><img src="/api/v1/blog/media/8dfc098b-a5f4-4cab-a89d-a87b4dc51af9">`
	got := sanitizeBody(input)
	for _, want := range []string{
		`<h2 style="text-align: center">`, "<strong>", "<u>", "<s>", "<mark>", "<code>",
		`<ol start="3">`, "<blockquote>", "<pre>", "<hr", `src="/api/v1/blog/media/8dfc098b-a5f4-4cab-a89d-a87b4dc51af9"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitized body lost %q: %s", want, got)
		}
	}
}

func TestSanitizeBodyRemovesScriptsAndHandlers(t *testing.T) {
	input := `<p onclick="steal()">hi</p><script>alert(1)</script><img src="x" onerror="alert(1)">` +
		`<a href="javascript:alert(1)">bad</a><a href="https://example.com">ok</a><iframe src="https://evil"></iframe>` +
		`<p style="color: red; text-align: left">t</p>`
	got := sanitizeBody(input)
	for _, banned := range []string{"onclick", "<script", "onerror", "javascript:", "<iframe", "color: red", `src="x"`} {
		if strings.Contains(got, banned) {
			t.Errorf("sanitized body kept %q: %s", banned, got)
		}
	}
	if !strings.Contains(got, `rel="nofollow`) || !strings.Contains(got, `target="_blank"`) {
		t.Errorf("external link not hardened: %s", got)
	}
}
