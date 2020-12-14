package main

import (
	"bufio"
	"io"
	"os"
	"testing"
)

func TestRoundUp2(t *testing.T) {
	tt := []struct {
		in, want int
	}{
		{100, 128},
		{256, 256},
		{257, 512},
		{1100, 2048},
		{3400, 4096},
	}
	for _, tc := range tt {
		if got := roundupPowerOf2(tc.in); got != tc.want {
			t.Fatalf("roundupPowerOf2(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestRingCount(t *testing.T) {
	tt := []struct {
		r, w               int
		cnt, space         int
		cntToEnd, spcToEnd int
	}{
		{0, 0, 0, 7, 0, 7},
		{2, 0, 6, 1, 6, 1},
		{4, 0, 4, 3, 4, 3},
		{6, 1, 3, 4, 2, 4},
		{7, 7, 0, 7, 0, 1},
	}
	for _, tc := range tt {
		rr := Ring{buf: make([]byte, 8), r: tc.r, w: tc.w}
		if got := rr.count(); got != tc.cnt {
			t.Fatalf("count([%d, %d]) = %v, want %v", tc.r, tc.w, got, tc.cnt)
		}
		if got := rr.free(); got != tc.space {
			t.Fatalf("free([%d, %d]) = %v, want %v", tc.r, tc.w, got, tc.space)
		}
		if got := rr.countToEnd(); got != tc.cntToEnd {
			t.Fatalf("countToEnd([%d, %d]) = %v, want %v", tc.r, tc.w, got, tc.cntToEnd)
		}
		if got := rr.freeToEnd(); got != tc.spcToEnd {
			t.Fatalf("freeToEnd([%d, %d]) = %v, want %v", tc.r, tc.w, got, tc.spcToEnd)
		}
	}
}

func TestRingWrite(t *testing.T) {
	tt := []struct {
		in     []byte
		buflen int
		err    bool
	}{
		{nil, 0, true},
		{[]byte{}, 0, true},
		{[]byte{1}, 0, true},
		{make([]byte, 99), 128, false},
		{make([]byte, 300), 512, false},
		{make([]byte, 1024), 2048, false},
	}
	for _, tc := range tt {
		var w Ring
		n, err := w.Write(tc.in)
		if err != nil {
			if tc.err == false {
				t.Fatalf("Write: %v, want nil", err)
			}
			continue
		}
		if n != len(tc.in) {
			t.Fatalf("Write: %d, want %d", n, len(tc.in))
		}
		if len(w.buf) != tc.buflen {
			t.Fatalf("buflen: %d, want %d", len(w.buf), tc.buflen)
		}
	}
}

func TestRingRead(t *testing.T) {
	tt := []struct {
		buf  []byte
		r, w int
		want string
	}{
		{[]byte("abcdefghijklmnop"), 0, 3, "abc"},
		{[]byte("abcdefghijklmnop"), 9, 14, "klmn"},
		{[]byte("abcdefghijklmnop"), 14, 2, "opab"},
		{[]byte("abcdefghijklmnop"), 14, 3, "pabc"},
		{[]byte("abcdefghijklmnop"), 15, 4, "abcd"},
	}
	for _, tc := range tt {
		rr := Ring{
			buf: tc.buf,
			r:   tc.r,
			w:   tc.w,
		}
		p := make([]byte, len(tc.want))
		n, err := rr.Read(p)
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if got := string(p[:n]); got != tc.want {
			t.Fatalf("Read() = %q, want %q", got, tc.want)
		}
	}
}

func TestWriteByte(t *testing.T) {
	tt := []struct {
		r, w  int
		bytes string
		want  string
	}{
		{0, 6, "xyz", "fxyz"},
		{9, 14, "skb,mb", ",mb"},
		// {14, 2, "xyz", "opab"},
		// {14, 3, "xyz", "pabc"},
		// {15, 4, "xyz", "abcd"},
	}
	buf := []byte("abcdefghijklmnop")
	for _, tc := range tt {
		rr := Ring{
			buf: buf,
			r:   tc.r,
			w:   tc.w,
		}
		for i := range tc.bytes {
			rr.WriteByte(tc.bytes[i])
		}
		p := make([]byte, len(tc.want))
		n, err := rr.Read(p)
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if got := string(p[:n]); got != tc.want {
			t.Fatalf("Read() = %q, want %q", got, tc.want)
		}
	}
}

func TestRead(t *testing.T) {
	f, err := os.Open("/etc/login.defs")
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(f)
	p := make([]byte, 512)
	for {
		_ = p
		n, err := r.ReadByte()
		if err != nil {
			t.Fatalf("err: %v, n: %d", err, n)
		}
	}
}
