//go:build !linux

package cpa

func readConfigHostFile(string) ([]byte, error) { return nil, ErrProofUnavailable }
