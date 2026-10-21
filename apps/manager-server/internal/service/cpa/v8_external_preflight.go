package cpa

// ValidV8ManagementKey shares the production read-only CPA v8 input guard.
// This does not authenticate the key; only the bounded CPA safe-read can.
func ValidV8ManagementKey(key string) bool { return validManagementKey(key) }
