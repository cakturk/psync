package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"fmt"
	"io"
	"io/ioutil"
	"reflect"
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
	err error
}

// moves window forward one byte at a time
func (r *RollingReader) Read(p []byte) (n int, err error) {
	if r.err != nil {
		return 0, r.err
	}
	copy(p, r.buf[:r.len])

	// move forward sliding window
	copy(r.buf, r.buf[1:])
	b, err := r.r.ReadByte()
	if err != nil {
		r.err = err
	}
	r.buf[len(r.buf)-1] = b
	return r.len, nil
}

func TestRollingReader(t *testing.T) {
	var tt = []struct {
		in     string
		window int
		want   []string
	}{
		{
			"abcdefghij",
			4,
			[]string{
				"abcd", "bcde", "cdef", "defg",
				"efgh", "fghi", "ghij",
			},
		},
		{
			"Plan 9 from outer space",
			4,
			[]string{
				"Plan", "lan ", "an 9", "n 9 ", " 9 f",
				"9 fr", " fro", "from", "rom ", "om o",
				"m ou", " out", "oute", "uter", "ter ",
				"er s", "r sp", " spa", "spac", "pace",
			},
		},
		{
			"xyz",
			4,
			[]string{"xyz"},
		},
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
				return sl, err
			}
			sl = append(sl, string(buf[:n]))
		}
	}
	for _, tc := range tt {
		got, err := read(tc.in, tc.window)
		if err != nil && err != io.EOF {
			t.Fatalf("failed: %v", err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("in: %q, got: %q, want: %q", tc.in, got, tc.want)
		}

	}
}

func TestFunction(t *testing.T) {
	b := [][]byte{
		{1, 2, 3, 4},
		{1, 2, 3, 4},
	}
	_ = b
}