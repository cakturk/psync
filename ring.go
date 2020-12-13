package main

import "errors"

type Ring struct {
	buf  []byte
	r, w uint
}

func (r *Ring) Write(p []byte) (n int, err error) {
	if len(p) >= len(r.buf)-1 {
		return 0, errors.New("ring: invalid use of Write")
	}
	panic("not implemented") // TODO: Implement
}

func (r *Ring) Read(p []byte) (n int, err error) {
	panic("not implemented") // TODO: Implement
}

func (r *Ring) WriteByte(c byte) error {
	panic("not implemented") // TODO: Implement
}

func (r *Ring) count() int {
	return int((r.w - r.r) & (uint(len(r.buf)) - 1))
}

func (r *Ring) countToEnd() int {
	end := uint(len(r.buf)) - r.r
	n := ((r.w + end) & (uint(len(r.buf) - 1)))
	if n < end {
		return int(n)
	}
	return int(end)
}

// r.w - head
// r.r - tail
func (r *Ring) free() int {
	return int((r.r - (r.w + 1)) & uint(len(r.buf)-1))
}

func (r *Ring) freeToEnd() int {
	end := uint(len(r.buf)) - 1 - r.w
	n := ((r.r + end) & (uint(len(r.buf) - 1)))
	if n <= end {
		return int(n)
	}
	return int(end) + 1
}
