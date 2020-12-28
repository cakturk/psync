package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log"
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
	r.buf = make([]byte, roundupPowerOf2(len(p)*2))
	r.r = 0
	r.w = len(p)
	return copy(r.buf, p), nil
}

// Discard skips the next n bytes
func (r *Ring) Discard(n int) (err error) {
	if n > r.count() {
		return errors.New("ring: short buffer")
	}
	r.r = (r.r + n) & (len(r.buf) - 1)
	return nil
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

func NewBring(r io.Reader, blockSize int) Bring {
	return Bring{
		r:         bufio.NewReader(r),
		blockSize: blockSize,
	}
}

type Bring struct {
	r         *bufio.Reader
	buf       bytes.Buffer
	tmp       bytes.Reader
	blockSize int
}

func (b *Bring) Read(p []byte) (n int, err error) {
	r := io.TeeReader(b.r, &b.buf)
	return r.Read(p)
}

// ReadByte reads and returns the next byte from the buffer.
func (b *Bring) ReadByte() (byte, error) {
	c, err := b.r.ReadByte()
	if err != nil {
		return c, err
	}
	err = b.buf.WriteByte(c)
	return c, err
}

func (b *Bring) Head() io.Reader {
	p := b.buf.Bytes()
	n := len(p) - b.blockSize
	if n <= 0 {
		return &b.tmp
	}
	b.buf.Next(len(p) - b.blockSize)
	b.tmp.Reset(p[:len(p)-b.blockSize])
	log.Printf("Bring:Head: %q", p[:len(p)-b.blockSize])
	return &b.tmp
}

func (b *Bring) HeadPeek() []byte {
	p := b.buf.Bytes()
	return p[:len(p)-b.blockSize]
}

func (b *Bring) HeadLen() int64 {
	len := b.buf.Len() - b.blockSize
	if len < 0 {
		len = 0
	}
	return int64(len)
}

func (b *Bring) Tail() io.Reader {
	p := b.buf.Bytes()
	b.tmp.Reset(p[len(p)-b.blockSize:])
	return &b.tmp
}

func (b *Bring) TailPeek() []byte {
	p := b.buf.Bytes()
	return p[len(p)-b.blockSize:]
}

func (b *Bring) Buffered() io.Reader {
	p := b.buf.Bytes()
	b.tmp.Reset(p)
	return &b.tmp
}

func (b *Bring) BufferedLen() int64 {
	return int64(b.buf.Len())
}

func (b *Bring) Skip() {
	b.buf.Next(b.buf.Len() - b.blockSize)
}
