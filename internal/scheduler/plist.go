package scheduler

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"
)

// LaunchDefinition encodes every path as XML text, never as plist markup.
func LaunchDefinition(label, owner string, argv []string, seconds int) (string, error) {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	e := xml.NewEncoder(&b)
	e.Indent("", "  ")
	if err := e.EncodeToken(xml.Directive(`DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"`)); err != nil {
		return "", err
	}
	token := func(t xml.Token) error { return e.EncodeToken(t) }
	start := func(n string) error { return token(xml.StartElement{Name: xml.Name{Local: n}}) }
	end := func(n string) error { return token(xml.EndElement{Name: xml.Name{Local: n}}) }
	text := func(n, v string) error { return e.EncodeElement(v, xml.StartElement{Name: xml.Name{Local: n}}) }
	if err := token(xml.StartElement{Name: xml.Name{Local: "plist"}, Attr: []xml.Attr{{Name: xml.Name{Local: "version"}, Value: "1.0"}}}); err != nil {
		return "", err
	}
	_ = start("dict")
	if err := token(xml.Comment(" VEYRA owner=" + owner + " ")); err != nil {
		return "", err
	}
	for _, kv := range [][2]string{{"Label", label}} {
		_ = text("key", kv[0])
		_ = text("string", kv[1])
	}
	_ = text("key", "ProgramArguments")
	_ = start("array")
	for _, a := range argv {
		if err := text("string", a); err != nil {
			return "", err
		}
	}
	_ = end("array")
	_ = text("key", "StartInterval")
	_ = text("integer", strconv.Itoa(seconds))
	_ = text("key", "RunAtLoad")
	_ = start("true")
	_ = end("true")
	_ = end("dict")
	_ = end("plist")
	if err := e.Flush(); err != nil {
		return "", err
	}
	// launchd's XPC plist parser requires the canonical empty boolean form;
	// plutil also accepts encoding/xml's expanded <true></true> representation.
	return strings.ReplaceAll(b.String(), "<true></true>", "<true/>") + "\n", nil
}
