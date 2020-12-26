package main

import (
	"errors"
	"io"
)

// Ring provides power-of-2 sized ring (circular) buffer
type Ring struct {
	buf  []byte
	r, w int
}

func (r *Ring) Write(p []byte) (n int, err error) {
	if len(p) <= 2 {
		return 0, errors.New("ring: short write")
	}
	r.buf = make([]byte, roundupPowerOf2(len(p)+1))
	r.r = 0
	r.w = len(p)
	return copy(r.buf, p), nil
}

func (r *Ring) Read(p []byte) (n int, err error) {
	if len(r.buf) == 0 {
		return 0, io.EOF
	}
	count := r.count()
	if count == 0 {
		return 0, io.EOF
	}
	rr := r.r
	if dr := count - len(p); dr > 0 {
		// advance read pointer
		// - - - - r - - - - - - - w
		// - - w - - - - - r - - - -
		// - - - - - - | - - - - - |
		rr = (rr + dr) & (len(r.buf) - 1)
		count = len(p)
	}
	n = copy(p, r.buf[rr:rr+countToEnd(rr, r.w, len(r.buf))])
	rr = (rr + n) & (len(r.buf) - 1)
	count -= n
	if count > 0 {
		n += copy(p[n:], r.buf[rr:rr+count])
	}
	return n, nil
}

// WriteByte writes a single byte.
func (r *Ring) WriteByte(c byte) error {
	r.buf[r.w] = c
	r.r = (r.r + 1) & (len(r.buf) - 1)
	r.w = (r.w + 1) & (len(r.buf) - 1)
	return nil
}

func (r *Ring) count() int {
	return int((r.w - r.r) & ((len(r.buf)) - 1))
}

func (r *Ring) countToEnd() int {
	return countToEnd(r.r, r.w, len(r.buf))
}

func countToEnd(r, w, len int) int {
	end := len - r
	n := ((w + end) & (len - 1))
	if n < end {
		return int(n)
	}
	return int(end)
}

// r.w - head
// r.r - tail
func (r *Ring) free() int {
	return int((r.r - (r.w + 1)) & (len(r.buf) - 1))
}

func (r *Ring) freeToEnd() int {
	end := (len(r.buf)) - 1 - r.w
	n := ((r.r + end) & (len(r.buf) - 1))
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
