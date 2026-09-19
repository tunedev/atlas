package tools

import (
	"fmt"
	"io"
)

// readLimited reads at most maxBytes from r and errors if there was more.
// It reads one byte past the limit so an exactly-at-limit body, which is
// legitimate, can be told apart from an over-limit one, which is not: a
// plain io.LimitReader would let both return the same short read.
func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response exceeds max size of %d bytes", maxBytes)
	}
	return body, nil
}
