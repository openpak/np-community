// Package xmpp serves the firmware's persistent NP stream behind a TLS proxy.
package xmpp

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

const streamNS = "http://etherx.jabber.org/streams"
const saslNS = "urn:ietf:params:xml:ns:xmpp-sasl"
const bindNS = "urn:ietf:params:xml:ns:xmpp-bind"
const sessionNS = "urn:ietf:params:xml:ns:xmpp-session"
const rosterNS = "jabber:iq:roster"
const stanzaNS = "urn:ietf:params:xml:ns:xmpp-stanzas"
const clientNS = "jabber:client"

type node struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Text     string
	Children []node
}

func (n node) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}
func (n node) child(ns, name string) *node {
	for i := range n.Children {
		c := &n.Children[i]
		if c.Name.Local == name && c.Name.Space == ns {
			return c
		}
	}
	return nil
}
func (n node) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name = n.Name
	for _, a := range n.Attrs {
		if a.Name.Local != "xmlns" && a.Name.Space != "xmlns" {
			start.Attr = append(start.Attr, a)
		}
	}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	if n.Text != "" {
		if err := e.EncodeToken(xml.CharData(n.Text)); err != nil {
			return err
		}
	}
	for _, c := range n.Children {
		if err := e.Encode(c); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}
func readNode(d *xml.Decoder, start xml.StartElement, depth int, budget *int) (node, error) {
	n := node{Name: start.Name, Attrs: start.Attr}
	if depth > 16 {
		return n, fmt.Errorf("stanza nesting limit")
	}
	for _, a := range start.Attr {
		*budget -= len(a.Value) + len(a.Name.Local)
	}
	for {
		t, e := d.Token()
		if e != nil {
			return n, e
		}
		*budget -= 16
		if *budget < 0 {
			return n, fmt.Errorf("stanza limit")
		}
		switch v := t.(type) {
		case xml.StartElement:
			c, e := readNode(d, v, depth+1, budget)
			if e != nil {
				return n, e
			}
			n.Children = append(n.Children, c)
		case xml.EndElement:
			return n, nil
		case xml.CharData:
			*budget -= len(v)
			if *budget < 0 {
				return n, fmt.Errorf("stanza limit")
			}
			n.Text += string(v)
		case xml.Directive:
			return n, fmt.Errorf("XML directive prohibited")
		}
	}
}
func nextNode(d *xml.Decoder) (node, error) {
	for {
		t, e := d.Token()
		if e != nil {
			return node{}, e
		}
		switch v := t.(type) {
		case xml.StartElement:
			budget := 32768
			return readNode(d, v, 0, &budget)
		case xml.EndElement:
			return node{}, io.EOF
		case xml.Directive:
			return node{}, fmt.Errorf("XML directive prohibited")
		}
	}
}
func streamStart(d *xml.Decoder) (xml.StartElement, error) {
	for {
		t, e := d.Token()
		if e != nil {
			return xml.StartElement{}, e
		}
		switch v := t.(type) {
		case xml.StartElement:
			if v.Name.Space != streamNS || v.Name.Local != "stream" {
				return v, fmt.Errorf("expected XMPP stream")
			}
			return v, nil
		case xml.Directive:
			return xml.StartElement{}, fmt.Errorf("XML directive prohibited")
		}
	}
}
func escaped(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
func attr(s string) string  { return `"` + escaped(s) + `"` }
func encoded(n node) string { b, _ := xml.Marshal(n); return string(b) }

// quotaReader bounds tokens too (including an oversized attribute or unterminated
// tag), before encoding/xml can allocate an unbounded token buffer.
type quotaReader struct {
	r         io.Reader
	remaining int64
}

func (q *quotaReader) Read(p []byte) (int, error) {
	if q.remaining <= 0 {
		return 0, fmt.Errorf("stanza byte limit")
	}
	if int64(len(p)) > q.remaining {
		p = p[:q.remaining]
	}
	n, e := q.r.Read(p)
	q.remaining -= int64(n)
	return n, e
}
func (q *quotaReader) ReadByte() (byte, error) {
	if q.remaining <= 0 {
		return 0, fmt.Errorf("stanza byte limit")
	}
	var p [1]byte
	n, e := q.r.Read(p[:])
	q.remaining -= int64(n)
	if n == 1 {
		return p[0], nil
	}
	return 0, e
}
func (q *quotaReader) reset() { q.remaining = 64 << 10 }
