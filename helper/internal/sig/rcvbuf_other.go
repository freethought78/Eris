//go:build !unix

package sig

import "errors"

func getRcvBuf(uintptr) (int, error) { return 0, errors.New("not supported") }
