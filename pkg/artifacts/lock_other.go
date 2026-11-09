//go:build !unix

package artifacts

import (
	"github.com/pkg/errors"
	"os"
)

func lockNamespace(string) (*os.File, error) {
	return nil, errors.New("artifact namespace locking is not supported on this platform")
}
func unlockNamespace(f *os.File) error { return f.Close() }
