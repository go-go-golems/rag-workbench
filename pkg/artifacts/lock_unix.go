//go:build unix

package artifacts

import (
	"github.com/pkg/errors"
	"os"
	"syscall"
)

func lockNamespace(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.Wrap(err, "artifact namespace is already owned")
	}
	return f, nil
}
func unlockNamespace(f *os.File) error { return f.Close() }
