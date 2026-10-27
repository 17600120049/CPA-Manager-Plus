package cpa

import (
	"errors"
	"encoding/json"
	"strings"
	"testing"
)

// TestGatewayPathTableCoversAcceptedInventory pins the accepted r3 scope: 39
// writable leaves across 11 page groups plus 5 whole-family rule lists.
func TestGatewayPathTableCoversAcceptedInventory(t *testing.T) {
	if len(gatewayLeaves) != 39 {
		t.Fatalf("leaf count %d", len(gatewayLeaves))
	}
	if len(ConfigLeafPaths()) != len(gatewayLeaves) {
		t.Fatal("leaf paths drifted")
	}
	seen := map[string]string{}
	for _, group := range gatewayGroups {
		for _, path := range group.Leaves {
			leaf, ok := LookupConfigLeaf(path)
			if !ok {
				t.Fatalf("%s: group %s names an unknown leaf", path, group.ID)
			}
			if leaf.Group != group.ID {
				t.Fatalf("%s: leaf group %s, group table %s", path, leaf.Group, group.ID)
			}
			if !strings.HasPrefix(path, group.Prefix+".") {
				t.Fatalf("%s: outside group prefix %s", path, group.Prefix)
			}
			if other, dup := seen[path]; dup {
				t.Fatalf("%s: in %s and %s", path, other, group.ID)
			}
			seen[path] = group.ID
		}
	}
	if len(seen) != len(gatewayLeaves) {
		t.Fatalf("groups cover %d of %d leaves", len(seen), len(gatewayLeaves))
	}
	if got := len(ConfigGroupIDs()); got != 11 {
		t.Fatalf("group count %d", got)
	}
	if got := len(ConfigRuleFamilyPaths()); got != 5 {
		t.Fatalf("rule family count %d", got)
	}
	for _, family := range []struct {
		name   string
		raw    bool
		filter bool
	}{
		{"default", false, false}, {"default-raw", true, false}, {"override", false, false},
		{"override-raw", true, false}, {"filter", false, true},
	} {
		entry, ok := LookupConfigRuleFamily(family.name)
		if !ok || entry.Raw != family.raw || entry.Filter != family.filter ||
			entry.Path != "requests.payload."+family.name {
			t.Fatalf("family %s: %+v", family.name, entry)
		}
	}
}

// TestPrivateProfileStaysFrozen proves the page table cannot widen the private
// H1 profile: the Host keeps exactly its six leaves.
func TestPrivateProfileStaysFrozen(t *testing.T) {
	want := []string{
		"requests.nonstream-keepalive-interval",
		"requests.passthrough-headers",
		"requests.streaming.bootstrap-retries",
		"requests.streaming.keepalive-seconds",
		"routing.retry.max-retry-interval",
		"routing.retry.request-retry",
	}
	got := PrivateConfigLeafPaths()
	if len(got) != len(want) {
		t.Fatalf("private profile %v", got)
	}
	for index, path := range want {
		if got[index] != path {
			t.Fatalf("private profile %v", got)
		}
		if err := ValidatePrivateConfigLeafValue(path, sampleValue(path)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if err := ValidatePrivateConfigLeafValue("requests.proxy-url", "http://proxy.test:8080"); err == nil {
		t.Fatal("private profile widened to a page leaf")
	}
	if err := ValidatePrivateConfigLeafValue("routing.strategy", "round-robin"); err == nil {
		t.Fatal("private profile widened to a string leaf")
	}
}

func sampleValue(path string) any {
	leaf, _ := LookupConfigLeaf(path)
	switch leaf.Type {
	case ConfigLeafInt:
		return 1
	case ConfigLeafBool:
		return true
	default:
		return "value"
	}
}

// TestLeafValuesAreShapesOnly proves CPAMP never restates a CPA bound: a value
// CPA accepts is a shape, and CPA owns the range.
func TestLeafValuesAreShapesOnly(t *testing.T) {
	for _, tc := range []struct {
		path  string
		value any
		ok    bool
	}{
		{"routing.retry.request-retry", 11, true},
		{"routing.retry.request-retry", 0, true},
		{"routing.retry.request-retry", -1, true},
		{"requests.streaming.keepalive-seconds", 301, true},
		{"routing.retry.request-retry", "0", false},
		{"routing.retry.request-retry", 1.5, false},
		{"routing.retry.request-retry", nil, false},
		{"requests.passthrough-headers", true, true},
		{"requests.passthrough-headers", 1, false},
		{"routing.strategy", "weighted-round-robin", true},
		{"routing.strategy", 1, false},
		{"multimedia.disable-image-generation", true, true},
		{"multimedia.disable-image-generation", false, true},
		{"multimedia.disable-image-generation", "chat", true},
		{"multimedia.disable-image-generation", "passthrough", true},
		{"multimedia.disable-image-generation", "disabled", false},
		{"multimedia.gpt-image-2-base-model", "gpt-5.4-mini", true},
	} {
		err := ValidateConfigLeafValue(tc.path, tc.value)
		if (err == nil) != tc.ok {
			t.Fatalf("%s=%v ok=%v err=%v", tc.path, tc.value, tc.ok, err)
		}
	}
	raw, err := ParseConfigLeafValue("multimedia.disable-image-generation", json.RawMessage(`"chat"`))
	if err != nil || raw != "chat" {
		t.Fatal(raw, err)
	}
	if _, err = ParseConfigLeafValue("multimedia.disable-image-generation", json.RawMessage(`null`)); err == nil {
		t.Fatal("null accepted as a leaf value")
	}
	if _, err = ParseConfigLeafValue("routing.retry.request-retry", json.RawMessage(`{}`)); err == nil {
		t.Fatal("object accepted as a scalar")
	}
	if _, err = ParseConfigLeafValue("unknown.path", json.RawMessage(`1`)); err == nil {
		t.Fatal("unknown path accepted")
	}
}

// TestProxyURLCredentialsAreRejected is one of the two product rules CPAMP owns.
func TestProxyURLCredentialsAreRejected(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{"http://proxy.test:8080", true},
		{"https://user:pass@proxy.test:8080", false},
		{"socks5://user@proxy.test:1080", false},
		{"socks5h://proxy.test:1080", true},
		{"ftp://proxy.test", false},
		{"proxy.test:8080", false},
		{"", false},
		{" http://proxy.test", false},
	} {
		err := ValidateProxyURL(tc.value)
		if (err == nil) != tc.ok {
			t.Fatalf("%q ok=%v err=%v", tc.value, tc.ok, err)
		}
	}
	if err := ValidateProxyURL(1); err == nil {
		t.Fatal("non-string proxy accepted")
	}
}

// TestRuleFamiliesRejectDuplicatePathsAndBadShapes is the other product rule.
func TestRuleFamiliesRejectDuplicatePathsAndBadShapes(t *testing.T) {
	valid := map[string]string{
		"default":      `[{"models":[{"name":"gpt-*","protocol":"codex","headers":{"X-Tier":"gold"},"exist":["metadata"]}],"params":{"reasoning.effort":"high","temperature":0.2}}]`,
		"default-raw":  `[{"models":[{"name":"gpt-*"}],"params":{"response_format":"{\"type\":\"json_object\"}"}}]`,
		"override":     `[{"models":[{"name":"*-5","from-protocol":"responses","match":[{"metadata.client":"x"}],"not-exist":["tools"]}],"params":{"top_p":1}}]`,
		"override-raw": `[{"models":[{"name":"*"}],"params":{}}]`,
		"filter":       `[{"models":[{"name":"gpt-*"}],"params":["metadata.client","metadata.tags"]}]`,
	}
	for family, body := range valid {
		if _, err := ParseConfigRules(family, json.RawMessage(body)); err != nil {
			t.Fatalf("%s: %v", family, err)
		}
	}
	invalid := map[string]struct {
		family string
		body   string
	}{
		"duplicate filter path":   {"filter", `[{"models":[{"name":"*"}],"params":["a","a"]}]`},
		"duplicate exist path":    {"default", `[{"models":[{"name":"*","exist":["a","a"]}],"params":{}}]`},
		"duplicate match path":    {"default", `[{"models":[{"name":"*","match":[{"a":1},{"a":2}]}],"params":{}}]`},
		"folded duplicate header": {"default", `[{"models":[{"name":"*","headers":{"X-A":"1","x-a":"2"}}],"params":{}}]`},
		"raw value not json":      {"default-raw", `[{"models":[{"name":"*"}],"params":{"a":"{"}}]`},
		"raw value not string":    {"default-raw", `[{"models":[{"name":"*"}],"params":{"a":{}}}]`},
		"filter params not array": {"filter", `[{"models":[{"name":"*"}],"params":{"a":"b"}}]`},
		"params not object":       {"default", `[{"models":[{"name":"*"}],"params":"[]"}]`},
		"unknown condition":       {"default", `[{"models":[{"name":"*","when":[]}],"params":{}}]`},
		"blank model name":        {"default", `[{"models":[{"name":" "}],"params":{}}]`},
		"unknown rule key":        {"default", `[{"models":[{"name":"*"}],"params":{},"note":"x"}]`},
		"empty body":              {"default", ``},
	}
	for name, tc := range invalid {
		if _, err := ParseConfigRules(tc.family, json.RawMessage(tc.body)); err == nil {
			t.Fatalf("%s (%s): invalid rules accepted", name, tc.family)
		}
	}
	if _, err := ParseConfigRules("filter", json.RawMessage(`[]`)); err != nil {
		t.Fatalf("empty family rejected: %v", err)
	}
	if _, err := ParseConfigRules("unknown", json.RawMessage(`[]`)); !errors.Is(err, ErrInvalidConfigPath) {
		t.Fatal("unknown family accepted")
	}
}
