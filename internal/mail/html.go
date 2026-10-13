package mail

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// HTMLText turns an HTML part into the text a reader sees in a browser, by
// the rules of the email spec (section 6): paragraphs and line breaks kept,
// lists marked, quotes prefixed with "> ", links written as "text
// (address)", and styles, scripts, comments, hidden text and tracking images
// left out.
//
// mimeCharset is "utf-8" when the MIME header named a charset (the body has
// been converted already), or "" to read the HTML's own declaration.
func HTMLText(body []byte, mimeCharset string) string {
	if mimeCharset == "" {
		if enc, name, _ := charset.DetermineEncoding(body, "text/html"); name != "utf-8" && enc != nil {
			if decoded, err := enc.NewDecoder().Bytes(body); err == nil {
				body = decoded
			}
		}
	}
	if !utf8.Valid(body) {
		body = bytes.ToValidUTF8(body, []byte("\ufffd"))
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	c := &converter{}
	c.node(doc)
	return tidy(c.b.String())
}

// converter writes text while walking the parsed document.
type converter struct {
	b         strings.Builder
	pending   int  // line breaks owed before the next text: 0, 1 or 2 (a blank line)
	pendingQ  int  // the quote depth the owed blank lines belong to
	space     bool // a collapsed space owed before the next text
	lineStart bool // nothing but the line's prefix written on this line
	sep       string
	quote     int
	lists     []list
	marker    string // a list item's marker, owed before its first text
	pre       int
	words     int  // runes written from text nodes (not image descriptions)
	nbsp      int  // non-breaking spaces seen
	zeroFont  bool // inside an element whose text has a font size of zero
}

type list struct {
	ordered bool
	next    int
}

// skipped elements are left out with everything in them.
var skipped = map[string]bool{
	"head": true, "title": true, "style": true, "script": true, "noscript": true, "template": true,
	"svg": true, "math": true, "object": true, "embed": true, "iframe": true, "canvas": true, "audio": true, "video": true,
	"select": true, "datalist": true,
}

// lineBlocks start and end on a line of their own; paraBlocks also leave a
// blank line around them.
var (
	lineBlocks = map[string]bool{
		"div": true, "tr": true, "table": true, "section": true, "article": true, "header": true, "footer": true,
		"nav": true, "aside": true, "main": true, "address": true, "center": true, "form": true, "fieldset": true,
		"legend": true, "figure": true, "figcaption": true, "details": true, "summary": true, "caption": true,
		"dt": true, "dd": true, "tbody": true, "thead": true, "tfoot": true,
	}
	paraBlocks = map[string]bool{
		"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "hr": true, "dl": true,
	}
)

func (c *converter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		c.text(n.Data, true)
		return
	case html.CommentNode, html.DoctypeNode:
		return
	case html.ElementNode:
		c.element(n)
		return
	}
	c.children(n)
}

func (c *converter) children(n *html.Node) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		c.node(child)
	}
}

// element writes an element, keeping track of a zero font size: responsive
// layouts set font-size:0 on wrappers to remove gaps between blocks, and
// their content sets its own size again. Text is dropped only where the
// size in force is zero.
func (c *converter) element(n *html.Node) {
	was := c.zeroFont
	switch fontSize(compactStyle(n)) {
	case sizeZero:
		c.zeroFont = true
	case sizeAbsolute:
		c.zeroFont = false
	}
	c.elementWithin(n)
	c.zeroFont = was
}

const (
	sizeNone     = iota // the element sets no font size
	sizeZero            // font-size:0
	sizeRelative        // a size relative to the parent's, such as 0.9em: zero stays zero
	sizeAbsolute        // any other size
)

// fontSize reads the last font-size an element's compact style sets.
func fontSize(style string) int {
	result := sizeNone
	for i := 0; ; {
		j := strings.Index(style[i:], "font-size:")
		if j < 0 {
			return result
		}
		at := i + j
		i = at + 1
		if at > 0 && style[at-1] != ';' && style[at-1] != '"' && style[at-1] != '\'' {
			continue // part of another property's name
		}
		value := style[at+len("font-size:"):]
		if end := strings.IndexAny(value, ";!"); end >= 0 {
			value = value[:end]
		}
		switch {
		case value == "":
		case value[0] == '0' && (len(value) == 1 || (value[1] != '.' && (value[1] < '0' || value[1] > '9'))):
			result = sizeZero
		case strings.HasSuffix(value, "%") || (strings.HasSuffix(value, "em") && !strings.HasSuffix(value, "rem")) ||
			value == "smaller" || value == "larger" || value == "inherit":
			result = sizeRelative
		default:
			result = sizeAbsolute
		}
	}
}

func (c *converter) elementWithin(n *html.Node) {
	tag := n.Data
	if skipped[tag] || hidden(n) {
		return
	}
	switch tag {
	case "br":
		c.lineBreak()
		return
	case "img":
		c.image(n)
		return
	case "a":
		c.link(n)
		return
	case "pre":
		c.block(2)
		c.pre++
		c.children(n)
		c.pre--
		c.block(2)
		return
	case "blockquote":
		c.block(2)
		c.quote++
		c.children(n)
		c.quote--
		c.block(2)
		return
	case "ul", "ol":
		level := 2
		if len(c.lists) > 0 {
			level = 1
		}
		start := 1
		if v, err := strconv.Atoi(attr(n, "start")); err == nil {
			start = v
		}
		c.block(level)
		c.lists = append(c.lists, list{ordered: tag == "ol", next: start})
		c.children(n)
		c.lists = c.lists[:len(c.lists)-1]
		c.block(level)
		return
	case "li":
		c.block(1)
		if len(c.lists) > 0 {
			l := &c.lists[len(c.lists)-1]
			indent := strings.Repeat("  ", len(c.lists)-1)
			if l.ordered {
				c.marker = fmt.Sprintf("%s%d. ", indent, l.next)
				l.next++
			} else {
				c.marker = indent + "• "
			}
		}
		c.children(n)
		c.marker = ""
		c.block(1)
		return
	case "td", "th":
		if !c.lineStart && c.pending == 0 && c.b.Len() > 0 {
			c.sep = "\t"
		}
		c.children(n)
		return
	}
	level := 0
	switch {
	case paraBlocks[tag]:
		level = 2
		if tag == "p" && strings.Contains(strings.ToLower(attr(n, "class")), "msonormal") {
			level = 1 // Outlook's paragraphs have no margins: they are lines
		}
	case lineBlocks[tag]:
		level = 1
	}
	if level == 0 {
		c.children(n) // inline, including Outlook's <o:p> and other namespaced tags
		return
	}
	c.block(level)
	words, nbsp := c.words, c.nbsp
	c.children(n)
	if (tag == "p" || tag == "div") && c.words == words && c.nbsp > nbsp {
		level = 2 // a paragraph holding only &nbsp; is how mail programs write a blank line
	}
	c.block(level)
}

// block owes level line breaks before the next text, unless a list item's
// marker is still waiting: the item's first block joins its marker's line.
func (c *converter) block(level int) {
	if c.marker != "" || c.b.Len() == 0 {
		return
	}
	c.owe(level)
}

func (c *converter) owe(level int) {
	if c.pending == 0 {
		c.pendingQ = c.quote
	}
	c.pending = max(c.pending, level)
	c.pendingQ = min(c.pendingQ, c.quote)
	c.space, c.sep = false, ""
}

// lineBreak is <br>: a new line, and with the one before it a blank line.
func (c *converter) lineBreak() {
	if c.b.Len() == 0 {
		return
	}
	if c.pending == 0 {
		c.pendingQ = c.quote
	}
	c.pending = min(c.pending+1, 2)
	c.pendingQ = min(c.pendingQ, c.quote)
	c.space, c.sep = false, ""
}

func (c *converter) prefix(depth int) string { return strings.Repeat("> ", depth) }

// flush writes what is owed before text: line breaks, the quote prefix and a
// list marker.
func (c *converter) flush() {
	if c.b.Len() == 0 {
		c.pending = 0
		c.b.WriteString(c.prefix(c.quote))
		c.lineStart = true
	}
	if c.pending > 0 {
		for i := 1; i < c.pending; i++ {
			c.b.WriteByte('\n')
			c.b.WriteString(c.prefix(c.pendingQ))
		}
		c.b.WriteByte('\n')
		c.b.WriteString(c.prefix(c.quote))
		c.pending, c.lineStart, c.space, c.sep = 0, true, false, ""
	}
	if c.marker != "" {
		c.b.WriteString(c.marker)
		c.marker = ""
		c.lineStart, c.space = true, false
	}
}

// invisible characters are dropped: zero-width spaces and joiners, the byte
// order mark, soft hyphens and the combining grapheme joiner newsletters pad
// their hidden previews with.
func invisible(r rune) bool {
	switch r {
	case '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff', '\u00ad', '\u034f':
		return true
	}
	return false
}

// text writes a run of text, collapsing whitespace as a browser does, or
// keeping it inside <pre>. words is false for an image's description.
func (c *converter) text(s string, words bool) {
	if c.zeroFont && words {
		return // a zero font size: the reader sees nothing of it
	}
	if c.pre > 0 {
		for i, line := range strings.Split(s, "\n") {
			if i > 0 {
				c.flush()
				c.b.WriteByte('\n')
				c.b.WriteString(c.prefix(c.quote))
				c.lineStart = true
			}
			if line = strings.Map(func(r rune) rune {
				if invisible(r) {
					return -1
				}
				if r == '\u00a0' {
					return ' '
				}
				return r
			}, line); line != "" {
				c.flush()
				c.b.WriteString(line)
				c.lineStart = false
				if words {
					c.words += utf8.RuneCountInString(line)
				}
			}
		}
		return
	}
	for _, r := range s {
		switch {
		case invisible(r):
			continue
		case r == '\u00a0':
			c.nbsp++
			c.space = true
			continue
		case unicode.IsSpace(r):
			c.space = true
			continue
		}
		c.flush()
		switch {
		case c.sep != "" && !c.lineStart:
			c.b.WriteString(c.sep)
		case c.space && !c.lineStart:
			c.b.WriteByte(' ')
		}
		c.sep, c.space = "", false
		c.b.WriteRune(r)
		c.lineStart = false
		if words {
			c.words++
		}
	}
}

// tracking reports whether an image is a tracking pixel or a spacer.
func tracking(n *html.Node) bool {
	for _, name := range []string{"width", "height"} {
		if v := strings.TrimSpace(strings.TrimSuffix(attr(n, name), "px")); v == "0" || v == "1" {
			return true
		}
	}
	style := compactStyle(n)
	for _, rule := range []string{"width:1px", "height:1px", "width:0", "height:0"} {
		if hasRule(style, rule) {
			return true
		}
	}
	return false
}

func (c *converter) image(n *html.Node) {
	if tracking(n) {
		return
	}
	if alt := strings.Join(strings.Fields(attr(n, "alt")), " "); alt != "" {
		c.text("["+alt+"]", false)
	}
}

// maxLinkAddress is the longest address written after a link's text; longer
// ones are tracking redirects nobody reads.
const maxLinkAddress = 200

func (c *converter) link(n *html.Node) {
	href := strings.TrimSpace(attr(n, "href"))
	start, words := c.b.Len(), c.words
	c.children(n)
	if c.words == words || len(href) > maxLinkAddress {
		return // no text of its own, or only an image
	}
	scheme := ""
	if i := strings.Index(href, ":"); i > 0 {
		scheme = strings.ToLower(href[:i])
	}
	if scheme != "http" && scheme != "https" && scheme != "ftp" {
		return
	}
	if sameAddress(c.b.String()[start:], href) {
		return
	}
	c.text(" ("+href+")", false)
}

// sameAddress reports whether a link's text already is its address.
func sameAddress(text, href string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		for _, p := range []string{"https://", "http://", "ftp://", "www."} {
			s = strings.TrimPrefix(s, p)
		}
		return strings.TrimSuffix(s, "/")
	}
	return norm(text) == norm(href)
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if a.Key == name {
			return true
		}
	}
	return false
}

// compactStyle is an element's style attribute, lower-cased, without spaces.
func compactStyle(n *html.Node) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, attr(n, "style"))
}

// hasRule reports whether a compact style holds a rule, such as "width:0".
// A rule ending in a number only matches when the number ends there, so
// "font-size:0" does not match "font-size:0.9em".
func hasRule(style, rule string) bool {
	for i := 0; ; {
		j := strings.Index(style[i:], rule)
		if j < 0 {
			return false
		}
		at := i + j
		if at > 0 && style[at-1] != ';' && style[at-1] != '"' && style[at-1] != '\'' {
			i = at + 1 // inside another property, such as "line-height:0" for "height:0"
			continue
		}
		end := at + len(rule)
		if end == len(style) || (style[end] != '.' && (style[end] < '0' || style[end] > '9')) {
			return true
		}
		i = at + 1
	}
}

// hidden reports whether an element is hidden from the reader, as a
// newsletter's preview text is.
func hidden(n *html.Node) bool {
	if hasAttr(n, "hidden") {
		return true
	}
	style := compactStyle(n)
	if style == "" {
		return false
	}
	// mso-hide hides from Outlook only (its Outlook twin is in a comment);
	// a zero font size is handled as the inherited property it is.
	for _, rule := range []string{"display:none", "visibility:hidden", "max-height:0", "opacity:0"} {
		if hasRule(style, rule) {
			return true
		}
	}
	return false
}

// tidy trims trailing spaces from every line, leaves at most one blank line
// at a time, and trims the text.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := true // at the start: drop leading blank lines
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		empty := strings.Trim(line, "> ") == "" && !strings.ContainsAny(strings.TrimLeft(line, "> "), "\t")
		if empty {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.Trim(out[len(out)-1], "> ") == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
