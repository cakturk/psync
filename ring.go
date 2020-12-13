package main

import "errors"

type Ring struct {
	buf  []byte
	r, w uint
}

func (r *Ring) Write(p []byte) (n int, err error) {
	if len(p) <= 2 {
		return 0, errors.New("ring: short write")
	}
	r.buf = make([]byte, roundupPowerOf2(len(p)+1))
	return copy(r.buf, p), nil
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

func roundupPowerOf2(n int) int {
	v := uint32(n)
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return int(v)
}
