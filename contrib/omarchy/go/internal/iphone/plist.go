package iphone

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// ParsePlist decodes an XML property list (what `ideviceinfo -x` prints) into Go values:
// dict -> map[string]any, array -> []any, string, int64, float64, bool, time.Time, []byte.
func ParsePlist(data []byte) (any, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, errors.New("not a plist")
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "plist" {
			for {
				tok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				if se, ok := tok.(xml.StartElement); ok {
					return plistValue(dec, se)
				}
			}
		}
	}
}

// PlistDict is ParsePlist for a top-level dict.
func PlistDict(data []byte) (map[string]any, error) {
	v, err := ParsePlist(data)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("plist is not a dict")
	}
	return m, nil
}

func plistValue(dec *xml.Decoder, se xml.StartElement) (any, error) {
	switch se.Name.Local {
	case "dict":
		m := map[string]any{}
		key := ""
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "key" {
					s, err := text(dec)
					if err != nil {
						return nil, err
					}
					key = s
					continue
				}
				v, err := plistValue(dec, t)
				if err != nil {
					return nil, err
				}
				m[key] = v
			case xml.EndElement:
				return m, nil
			}
		}
	case "array":
		var a []any
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				v, err := plistValue(dec, t)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			case xml.EndElement:
				return a, nil
			}
		}
	case "true", "false":
		if err := dec.Skip(); err != nil {
			return nil, err
		}
		return se.Name.Local == "true", nil
	}
	s, err := text(dec)
	if err != nil {
		return nil, err
	}
	switch se.Name.Local {
	case "integer":
		return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	case "real":
		return strconv.ParseFloat(strings.TrimSpace(s), 64)
	case "date":
		return time.Parse(time.RFC3339, strings.TrimSpace(s))
	case "data":
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
	}
	return s, nil
}

// text reads character data up to the end of the current element.
func text(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return b.String(), nil
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			return b.String(), nil
		}
	}
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func num(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}
func boolean(m map[string]any, k string) bool { b, _ := m[k].(bool); return b }
