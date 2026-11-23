package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// encodeJSON marshals v as compact JSON that is pure ASCII, like Python's
// json.dumps(ensure_ascii=True): HTML characters are not escaped and every character outside
// printable ASCII becomes a \uXXXX escape (surrogate pairs above U+FFFF).
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return asciiOnly(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// asciiOnly rewrites every non-ASCII character, and DEL, in an encoded JSON document as an
// escape sequence. Such characters can only occur inside JSON strings, so this is safe.
func asciiOnly(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		b := in[i]
		if b < 0x7f {
			out = append(out, b)
			i++
			continue
		}
		r, size := utf8.DecodeRune(in[i:])
		i += size
		if r >= 0x10000 {
			r -= 0x10000
			out = fmt.Appendf(out, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			continue
		}
		out = fmt.Appendf(out, `\u%04x`, r)
	}
	return out
}
