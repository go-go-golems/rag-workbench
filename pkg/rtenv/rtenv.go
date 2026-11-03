// Package rtenv holds per-runtime host services shared by the ragwb module
// (pkg/ragjs) and the Go components installed into it (pkg/natives), without
// an import cycle between the two.
package rtenv

// Env is per-runtime host services for natives that hold resources (open
// indexes, temp directories). AddCloser registers a cleanup that runs when the
// runtime closes; it is never nil when handed to a native.
type Env struct {
	AddCloser func(func() error)
}
