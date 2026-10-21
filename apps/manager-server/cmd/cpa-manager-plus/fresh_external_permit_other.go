//go:build !linux

package main

// Non-Linux private profiles cannot inherit Linux/amd64 Host authority.
func loadPrivateFreshExternalPermit(string) (freshExternalPermit, bool) {
	return freshExternalPermit{}, false
}
