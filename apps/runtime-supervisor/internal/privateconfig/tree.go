// Package privateconfig implements only the opt-in, Host-owned B0/B1 boundary.
// CPA remains the writer. These checks are not an upstream CAS or a config API.
package privateconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrStop = errors.New("private configuration authority unavailable; STOP")

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hashValid(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func tree(b []byte) (*yaml.Node, error) {
	d := yaml.NewDecoder(strings.NewReader(string(b)))
	var n yaml.Node
	if d.Decode(&n) != nil || len(n.Content) != 1 || n.Content[0].Kind != yaml.MappingNode || d.Decode(new(yaml.Node)) != io.EOF {
		return nil, ErrStop
	}
	if _, e := canonical(n.Content[0]); e != nil {
		return nil, e
	}
	return n.Content[0], nil
}

// Include tags, missing/null, ordering of lists and all opaque/secret values.
// Comments and mapping order may change in CPA's YAML serialization.
func canonical(n *yaml.Node) (string, error) {
	if n == nil || n.Alias != nil || n.Anchor != "" {
		return "", ErrStop
	}
	parts := []string{strconv.Itoa(int(n.Kind)), n.Tag, n.Value}
	switch n.Kind {
	case yaml.MappingNode:
		if len(n.Content)%2 != 0 {
			return "", ErrStop
		}
		entries := map[string]string{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				return "", ErrStop
			}
			if _, exists := entries[k.Value]; exists {
				return "", ErrStop
			}
			v, e := canonical(n.Content[i+1])
			if e != nil {
				return "", e
			}
			entries[k.Value] = v
		}
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, k, entries[k])
		}
	case yaml.SequenceNode:
		for _, v := range n.Content {
			s, e := canonical(v)
			if e != nil {
				return "", e
			}
			parts = append(parts, s)
		}
	case yaml.ScalarNode:
	default:
		return "", ErrStop
	}
	b, e := json.Marshal(parts)
	return string(b), e
}

func leaf(n *yaml.Node, path ...string) *yaml.Node {
	for _, p := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i < len(n.Content); i += 2 {
			if n.Content[i].Value == p {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	return n
}
func set(n *yaml.Node, value *yaml.Node, path ...string) error {
	if n == nil || n.Kind != yaml.MappingNode || len(path) == 0 {
		return ErrStop
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value != path[0] {
			continue
		}
		if len(path) == 1 {
			n.Content[i+1] = value
			return nil
		}
		return set(n.Content[i+1], value, path[1:]...)
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: path[0]}
	if len(path) == 1 {
		n.Content = append(n.Content, key, value)
		return nil
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	n.Content = append(n.Content, key, child)
	return set(child, value, path[1:]...)
}

func validChange(before, after []byte, op Operation) bool {
	a, e := tree(before)
	if e != nil {
		return false
	}
	b, e := tree(after)
	if e != nil {
		return false
	}
	switch op.Scope {
	case "initial-key":
		old := leaf(a, "access", "api-keys")
		next := leaf(b, "access", "api-keys")
		if old == nil || old.Kind != yaml.SequenceNode || len(old.Content) != 0 ||
			next == nil || next.Kind != yaml.SequenceNode || len(next.Content) != 1 ||
			next.Content[0].Tag != "!!str" || digest([]byte(next.Content[0].Value)) != op.KeySHA256 {
			return false
		}
		if set(a, next, "access", "api-keys") != nil {
			return false
		}
	case "request-retry":
		next := leaf(b, "routing", "retry", "request-retry")
		if next == nil || next.Tag != "!!int" || next.Value != strconv.Itoa(op.Retry) ||
			set(a, next, "routing", "retry", "request-retry") != nil {
			return false
		}
	default:
		return false
	}
	x, e := canonical(a)
	if e != nil {
		return false
	}
	y, e := canonical(b)
	return e == nil && x == y
}
