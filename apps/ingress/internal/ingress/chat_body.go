package ingress

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const chatPath = "/v1/chat/completions"
const PrivateChatAdmissionPath = "/internal/fresh-chat-admission"
const maxChatBytes = 64 << 10

var errChat = errors.New("private chat unavailable")

// This private consumer accepts a complete, explicitly framed JSON body only.
// It never streams a partially inspected body or forwards client hop/auth hints.
func readChat(r *http.Request) ([]byte, error) {
	return readChatModel(r, "s1-proof-model")
}

func readChatModel(r *http.Request, allowedModel string) ([]byte, error) {
	if r.Method != http.MethodPost || r.URL.Path != chatPath || r.URL.EscapedPath() != chatPath ||
		r.RequestURI != chatPath || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.IsAbs() ||
		r.ContentLength <= 0 || r.ContentLength > maxChatBytes || len(r.TransferEncoding) != 0 || len(r.Trailer) != 0 ||
		len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Type") != "application/json" ||
		len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || len(r.Header.Get("Authorization")) != 80 {
		return nil, errChat
	}
	for _, h := range []string{"Content-Encoding", "Upgrade", "Expect", "X-API-Key", "X-Goog-API-Key", "X-CPA-Management-Key"} {
		if len(r.Header.Values(h)) != 0 {
			return nil, errChat
		}
	}
	if v := r.Header.Get("Connection"); v != "" && !strings.EqualFold(v, "close") && !strings.EqualFold(v, "keep-alive") {
		return nil, errChat
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxChatBytes+1))
	if err != nil || int64(len(body)) != r.ContentLength || !utf8.Valid(body) || !validSurrogates(body) {
		return nil, errChat
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := uniqueJSON(d, 0); err != nil || d.Decode(new(any)) != io.EOF {
		return nil, errChat
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil || !bytes.Equal(bytes.TrimSpace(root["stream"]), []byte("false")) {
		return nil, errChat
	}
	var model string
	if allowedModel == "" || json.Unmarshal(root["model"], &model) != nil || model != allowedModel {
		return nil, errChat
	}
	for name := range root {
		if (strings.EqualFold(name, "stream") && name != "stream") || (strings.EqualFold(name, "model") && name != "model") {
			return nil, errChat
		}
	}
	return body, nil
}

// Reject duplicate names at every depth, including escaped and case aliases.
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errChat
	}
	t, err := d.Token()
	if err != nil {
		return errChat
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			t, err = d.Token()
			if err != nil {
				return errChat
			}
			name, ok := t.(string)
			if !ok {
				return errChat
			}
			// Match encoding/json's Unicode field folding too (for example,
			// long s and ASCII s), not only ordinary upper/lowercase aliases.
			name = strings.Map(func(r rune) rune {
				min := r
				for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
					if folded < min {
						min = folded
					}
				}
				return min
			}, name)
			if seen[name] {
				return errChat
			}
			seen[name] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errChat
	}
	end, err := d.Token()
	if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return errChat
	}
	return nil
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. Reject those
// instead of allowing a downstream parser to assign different string values.
func validSurrogates(b []byte) bool {
	hex4 := func(v []byte) int {
		n := 0
		for _, c := range v {
			n *= 16
			switch {
			case c >= '0' && c <= '9':
				n += int(c - '0')
			case c >= 'a' && c <= 'f':
				n += int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				n += int(c-'A') + 10
			default:
				return -1
			}
		}
		return n
	}
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return false
		}
		if b[i] != 'u' {
			continue
		}
		if i+4 >= len(b) {
			return false
		}
		n := hex4(b[i+1 : i+5])
		if n < 0 {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low := hex4(b[i+3 : i+7])
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
