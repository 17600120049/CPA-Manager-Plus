//go:build !linux && !darwin

package legacyartifact

import "os"

type offlineLock struct{ file *os.File }

func acquireOfflineLock(string) (*offlineLock, error) { return nil, ErrOfflinePlatform }
func (l *offlineLock) close() error                   { return nil }
func offlineSingleLink(*os.File) bool                 { return false }
