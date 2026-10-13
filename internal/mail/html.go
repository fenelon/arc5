package mail

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// HTMLText turns an HTML part into readable text. mimeCharset is the
// charset the body is already in ("utf-8" when the MIME header named one),
// or "" to read it from the HTML's own declaration.
func HTMLText(body []byte, mimeCharset string) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "style" || n.Data == "script" || n.Data == "head") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return strings.Join(strings.Fields(b.String()), " ")
}
