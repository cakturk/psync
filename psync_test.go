package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"testing"
)

type sliceEncoder []interface{}

func (s *sliceEncoder) Encode(e interface{}) error {
	*s = append(*s, e)
	return nil
}

func TestChunkFile(t *testing.T) {
	// var buf bytes.Buffer
	// enc := gob.NewEncoder(&buf)
	enc := &sliceEncoder{}
	if err := chunkFile("foo", enc, 4); err != nil {
		t.Fatal(err)
	}
	t.Errorf("%#v", enc)
}

func TestMd(t *testing.T) {
	m := md5.New()
	s := m.Sum(nil)
	t.Errorf("%#v", s)
}

// x x x | x x x | x _ _ |
func TestHowMany(t *testing.T) {
	hm := func(x, y int) int {
		return ((x) + ((y) - 1)) / (y)
	}
	r := hm(6, 3)
	t.Errorf("r: %d", r)
}

func TestNilFieldEnc(t *testing.T) {
	type Foo struct {
		A int
		b *Foo
	}
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	err := enc.Encode(Foo{})
	if err != nil {
		t.Fatal(err)
	}
	err = enc.Encode(Foo{})
	if err != nil {
		t.Fatal(err)
	}
	err = enc.Encode(Foo{})
	if err != nil {
		t.Fatal(err)
	}
	t.Error(b.Len())
}

func TestSyncEnt(t *testing.T) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	err := enc.Encode(SyncEnt{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnc(t *testing.T) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	type Foo struct {
		ID   int
		Name string
		save int
	}
	err := enc.Encode(Foo{
		ID:   0x00AB,
		Name: "go away",
		save: 33,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteString("ohh no"); err != nil {
		t.Fatal(err)
	}
	var w Foo
	dec := gob.NewDecoder(&b)
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&w); err != nil {
		t.Fatal(err)
	}
	t.Errorf("%+v", w)
	if s, err := ioutil.ReadAll(&b); err == nil {
		t.Errorf("s: %q", s)
	}
}

func NewRollingReader(r *bufio.Reader, window int) (io.Reader, error) {
	var (
		n   int
		err error
	)
	p := make([]byte, window)
	if n, err = r.Read(p); err != nil {
		if err != io.EOF {
			return nil, err
		}
		if n == 0 {
			return nil, errShortRead
		}
	}
	// log.Printf("xyz: %s, err: %v, n: %v", p, err, n)
	rr := &RollingReader{
		r:   r,
		buf: p,
		len: n,
	}
	return rr, nil
}

// read from file -> write to -> wr
type RollingReader struct {
	r   *bufio.Reader
	buf []byte
	len int
}

// s     e
// x x x x x x x x
// h   t
// moves window forward one byte at a time
func (r *RollingReader) Read(p []byte) (n int, err error) {
	if r.len < len(r.buf) {
		fmt.Printf("burada\n")
		return copy(p, r.buf[:r.len]), nil
	}
	copy(r.buf, r.buf[1:])
	b, err := r.r.ReadByte()
	if err != nil {
		return 0, err
	}
	r.buf[len(r.buf)-1] = b
	return len(r.buf), nil
}

func TestRollingReader(t *testing.T) {
	var tt = []struct {
		in     string
		window int
		want   []string
	}{
		{"Plan 9 from outer space", 4, nil},
	}
	read := func(s string, w int) ([]string, error) {
		rr, err := NewRollingReader(bufio.NewReader(strings.NewReader(s)), w)
		if err != nil {
			return nil, fmt.Errorf("wtf: %w", err)
		}
		var sl []string
		buf := make([]byte, w)
		for {
			n, err := rr.Read(buf)
			if err != nil {
				return sl, fmt.Errorf("read: %w", err)
			}
			sl = append(sl, string(buf[:n]))
		}
	}
	_ = read
	for _, tc := range tt {
		got, err := read(tc.in, tc.window)
		if err != nil {
			t.Fatalf("failed: %v", err)
		}
		_ = err
		t.Errorf("in: %v, got: %v, want: %v", tc.in, got, tc.want)

	}
}

func TestFunction(t *testing.T) {
	b := [][]byte{
		{1, 2, 3, 4},
		{1, 2, 3, 4},
	}
	_ = b
}
