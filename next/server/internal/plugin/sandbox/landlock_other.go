//go:build !linux

package sandbox

import "errors"

// landlockABI: Landlock is Linux only.
func landlockABI() (int, error) { return 0, errors.ErrUnsupported }
