//go:build !unix

package terminal

import "os"

// tryFlock is a no-op where there is no flock: the Terminal.app tab path it
// serializes only ever runs on darwin.
func tryFlock(*os.File) (bool, error) { return true, nil }
