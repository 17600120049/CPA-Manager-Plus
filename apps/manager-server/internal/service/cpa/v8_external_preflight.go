package cpa

import (
	"net/url"
	"strconv"
	"strings"
)

// ValidV8SourcePort is an External admission-only guard for a syntactically
// normalized CPA v8 Base URL. It rejects impossible TCP port values before
// Fresh consumes the durable one-shot attempt. It does not change the pinned,
// independently accepted CPA safe-read implementation.
func ValidV8SourcePort(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	port := u.Port()
	if port == "" {
		return !strings.HasSuffix(u.Host, ":")
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 1 && value <= 65535
}

// ValidV8ManagementKey shares the production read-only CPA v8 input guard.
// This does not authenticate the key; only the bounded CPA safe-read can.
func ValidV8ManagementKey(key string) bool { return validManagementKey(key) }
