package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"hash"
	stdadler32 "hash/adler32"
	"io"
	"io/ioutil"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chmduquesne/rollinghash/adler32"
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
	t.Errorf("%#v", hex.EncodeToString(s))
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
	err := enc.Encode(SrcFile{})
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

func NewRollingReader(r *bufio.Reader, window int) (*RollingReader, error) {
	var (
		n   int
		err error
	)
	p := make([]byte, window+1)
	if n, err = r.Read(p); err != nil {
		if err != io.EOF {
			return nil, err
		}
		if n == 0 {
			return nil, errShortRead
		}
	}
	rr := &RollingReader{
		r:        r,
		buf:      p,
		len:      n,
		lastByte: -1,
	}
	return rr, nil
}

// read from file -> write to -> wr
type RollingReader struct {
	r        *bufio.Reader
	buf      []byte
	len      int
	lastByte int
	err      error
}

// moves window forward one byte at a time
func (r *RollingReader) Read(p []byte) (n int, err error) {
	if r.err != nil {
		return 0, r.err
	}
	copy(p, r.buf[:r.len])

	// move forward sliding window
	r.lastByte = int(r.buf[0])
	b, err := r.r.ReadByte()
	if err != nil {
		r.err = err
		return r.len, nil
	}
	copy(r.buf, r.buf[1:])
	r.buf[len(r.buf)-1] = b
	return r.len, nil
}

//     x     y
// a b c d e f g h i
//   0     1
func (r *RollingReader) Backup() error {
	log.Printf("calling backup: %v, lb: %d", r.err, r.lastByte)
	if r.err != nil {
		if r.err == io.EOF {
			r.err = nil
			return nil
		}
		return r.err
	}
	if r.lastByte < 0 {
		return fmt.Errorf("invalid use of Backup")
	}
	copy(r.buf[1:], r.buf[0:])
	r.buf[0] = byte(r.lastByte)
	r.lastByte = -1
	return nil
}

func TestBackup(t *testing.T) {
	var tt = []struct {
		in       string
		window   int
		backupAt int
		want     []string
	}{
		{
			"abcde",
			4,
			2,
			[]string{
				"abcd", "bcde",
			},
		},
	}
	read := func(s string, w int) ([]string, error) {
		rr, err := NewRollingReader(bufio.NewReader(strings.NewReader(s)), w)
		if err != nil {
			return nil, fmt.Errorf("wtf: %w", err)
		}
		var sl []string
		buf := make([]byte, w)
		count := 0
		for {
			n, err := rr.Read(buf)
			if count++; count == 1 {
				if err := rr.Backup(); err != nil {
					return sl, fmt.Errorf("backup: %w", err)
				}
			}
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

type mergeDscEnc []interface{}

func (s *mergeDscEnc) Encode(e interface{}) error {
	*s = append(*s, e)
	return nil
}

var (
	// 54 bytes
	orig = `01234567890abcdef
ghijklmnopqrstuvwxyz
Plan9FromBellLabs
`
	modified = `01234567890abcdef
ghijklmnop-modified-
Plan9FromBellLabs
`
)

type strSliceWriter []string

func (s *strSliceWriter) Write(p []byte) (n int, err error) {
	*s = append(*s, string(p))
	return len(p), nil
}

// stdlib adler32
// psync_test.go|343| "beam me " cf302a8
// psync_test.go|343| "up scott" 301f05da
// psync_test.go|343|        "y" 36720653

// rolling adler32
// psync_test.go|353| "beam me " cf302a8
// psync_test.go|353| "up scott" 301f05da
// psync_test.go|353| "y" 36720653

func TestRollingAdler32(t *testing.T) {
	buf := []byte("beam me up scotty")
	br := bufio.NewReader(bytes.NewReader(buf))
	sh := stdadler32.New()
	rh := adler32.New()
	mr := io.MultiWriter(rh, sh)
	if _, err := io.CopyN(mr, br, 8); err != nil {
		t.Fatal(err)
	}
	min := func(x, y int) int {
		if x < y {
			return x
		}
		return y
	}
	cur := buf[:min(8, len(buf))]
	for i := 1; ; i++ {
		if s0, s1 := rh.Sum32(), sh.Sum32(); s0 != s1 {
			t.Fatalf("checksum mismatch: %d == %d", s0, s1)
		}
		// t.Errorf("%q %x, %x", cur, rh.Sum32(), sh.Sum32())
		b, err := br.ReadByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		sh.Reset()
		cur = buf[i:min(i+8, len(buf))]
		if _, err := sh.Write(cur); err != nil {
			t.Fatal(err)
		}
		rh.Roll(b)
	}
}

func TestDoChunkFile(t *testing.T) {
	f := strings.NewReader(orig)
	var buf strSliceWriter
	tr := io.TeeReader(f, &buf)
	enc := sliceEncoder{}
	if err := doChunkFile(tr, &enc, 8); err != nil {
		t.Fatal(err)
	}
	for _, v := range enc {
		ch := v.(Chunk)
		t.Errorf("%v", &ch)
	}
	t.Fatalf("%#v", buf)
}

func TestMergeDesc(t *testing.T) {
	// f, err := os.Open("/etc/login.defs")
	f := strings.NewReader(orig)
	var err error
	if err != nil {
		t.Fatal(err)
	}
	src := &SrcFile{
		Path:      "",
		Uid:       0,
		Gid:       0,
		Mode:      0,
		Size:      0,
		Mtime:     time.Time{},
		chunkSize: 0,
		base: DstFile{
			ID:        0,
			ChunkSize: 8,
			Size:      0,
			chunks: map[uint32]ChunkWithID{
				0: {
					ID: 0,
					Chunk: Chunk{
						Rsum: 0,
						Sum:  []byte{},
					},
				},
			},
		},
	}
	enc := &mergeDscEnc{}
	if err = sendMergeDescs(f, src, enc); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("%#v", enc)
}

func TestExample(t *testing.T) {
	s := []byte("The quick brown fox jumps over the lazy dog")

	classic := hash.Hash32(adler32.New())
	rolling := adler32.New()

	// Window len
	n := 16

	// You MUST load an initial window into the rolling hash before being
	// able to roll bytes
	rolling.Write(s[:n])

	// Roll it and compare the result with full re-calculus every time
	for i := n; i < len(s); i++ {

		// Reset and write the window in classic
		classic.Reset()
		classic.Write(s[i-n+1 : i+1])

		if i == 18 {
			i += 8
			rolling.Reset()
			rolling.Write(s[i-n+1 : i+1])
			continue

		}
		// Roll the incoming byte in rolling
		rolling.Roll(s[i])

		t.Errorf("%v,%v: checksum %x\n", i, string(s[i-n+1:i+1]), rolling.Sum32())

		// Compare the hashes
		if classic.Sum32() != rolling.Sum32() {
			t.Fatalf("%v: expected %x, got %x",
				s[i-n+1:i+1], classic.Sum32(), rolling.Sum32())
		}
	}

	// Output:
	// he quick brown f: checksum 31e905d9
	// e quick brown fo: checksum 314805e0
	//  quick brown fox: checksum 30ea05f3
	// quick brown fox : checksum 34dc05f3
	// uick brown fox j: checksum 33b705ec
	// ick brown fox ju: checksum 325205ec
	// ck brown fox jum: checksum 31b105f0
	// k brown fox jump: checksum 317d05fd
	//  brown fox jumps: checksum 30d10605
	// brown fox jumps : checksum 34d50605
	// rown fox jumps o: checksum 34c60612
	// own fox jumps ov: checksum 33bb0616
	// wn fox jumps ove: checksum 32d6060c
	// n fox jumps over: checksum 316c0607
	//  fox jumps over : checksum 304405b9
	// fox jumps over t: checksum 3450060d
	// ox jumps over th: checksum 33fe060f
	// x jumps over the: checksum 33120605
	//  jumps over the : checksum 313e05ad
	// jumps over the l: checksum 353605f9
	// umps over the la: checksum 348505f0
	// mps over the laz: checksum 332905f5
	// ps over the lazy: checksum 32590601
	// s over the lazy : checksum 310905b1
	//  over the lazy d: checksum 2f7a05a2
	// over the lazy do: checksum 336a05f1
	// ver the lazy dog: checksum 326205e9
}
