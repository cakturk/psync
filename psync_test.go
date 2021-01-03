package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"hash"
	stdadler32 "hash/adler32"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/chmduquesne/rollinghash/adler32"
	"github.com/google/go-cmp/cmp"
)

type sliceEncoder []interface{}

func (s *sliceEncoder) Write(p []byte) (n int, err error) {
	panic("not implemented") // TODO: Implement
}

func (s *sliceEncoder) Encode(e interface{}) error {
	*s = append(*s, e)
	return nil
}

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

type mergeDscEnc []interface{}

func (s *mergeDscEnc) Write(p []byte) (n int, err error) {
	log.Printf("write invoked: %d, %q", len(p), p)
	*s = append(*s, p)
	return len(p), nil
}

func (s *mergeDscEnc) Encode(e interface{}) error {
	*s = append(*s, e)
	return nil
}

const (
	// 57 bytes
	orig = `01234567890abcdef
ghijklmnopqrstuvwxyz
Plan9FromBellLabs
`
	modified = `01234567890abcdef
ghijklmnop-modified-la
Plan9FromBellLabs
`
)

// psync_test.go|187| &main.mergeDscEnc{main.MergeDesc{ID:22, Typ:0x1,
// TotalSize:0}, 0x0, main.MergeReuse{ChunkID:0, NrChunks:3, Off:0}, 0x1,
// main.MergeBlob{Size:8, Off:24}, []uint8{0x6d, 0x6e, 0x6f, 0x70, 0x2d, 0x6d,
// 0x6f, 0x64}, 0x1, main.MergeBlob{Size:10, Off:32}, []uint8{0x69, 0x66, 0x69,
// 0x65, 0x64, 0x2d, 0x6c, 0x61, 0xa, 0x50}, 0x0, main.MergeReuse{ChunkID:5,
// NrChunks:3, Off:42}}
func TestMergeDesc2(t *testing.T) {
	f := strings.NewReader(modified)
	var err error
	if err != nil {
		t.Fatal(err)
	}
	digest := func(s string) []byte {
		m, err := hex.DecodeString(s)
		if err != nil {
			t.Fatalf("failed to decode string: %q", s)
		}
		return m
	}
	src := &SenderSrcFile{
		SrcFile: SrcFile{
			Path:  "",
			Uid:   0,
			Gid:   0,
			Mode:  0,
			Size:  int64(len(orig)),
			Mtime: time.Time{},
		},
		dst: SenderDstFile{
			DstFile: DstFile{
				ID:        0, // file id
				ChunkSize: 8,
				Size:      1,
			},
			chunks: map[uint32]SenderChunk{
				0x071c019d: {
					id:    0, // chunk id
					Chunk: Chunk{Rsum: 0x071c019d, Sum: digest("2e9ec317e197819358fbc43afca7d837")},
				},
				0x0a3a0291: {
					id:    1, // chunk id
					Chunk: Chunk{Rsum: 0x0a3a0291, Sum: digest("0971ea36560f190d33257a3722f2b08c")},
				},
				0x0c1402ea: {
					id:    2, // chunk id
					Chunk: Chunk{Rsum: 0x0c1402ea, Sum: digest("6f1adba1b07b8042ab76144a2bc98f86")},
				},
				0x0fb00385: {
					id:    3, // chunk id
					Chunk: Chunk{Rsum: 0x0fb00385, Sum: digest("a70900006e6c6e510d501865a9f65efd")},
				},
				0x0fc20328: {
					id:    4, // chunk id
					Chunk: Chunk{Rsum: 0x0fc20328, Sum: digest("aa7e6f7af8d9f4ce4bbe37c99645068a")},
				},
				0x0d790309: {
					id:    5, // chunk id
					Chunk: Chunk{Rsum: 0x0d790309, Sum: digest("7f75672f0f60125b9d78fc51fd5c3614")},
				},
				0x0d090302: {
					id:    6, // chunk id
					Chunk: Chunk{Rsum: 0x0d090302, Sum: digest("008f7a640603fa380ae5fa52eddb1f9f")},
				},
				0x000b000b: {
					id:    7, // chunk id
					Chunk: Chunk{Rsum: 0x000b000b, Sum: digest("68b329da9893e34099c7d8ad5cb9c940")},
				},
			},
		},
	}
	enc := &mergeDscEnc{}
	if err = sendMergeDescs(f, 22, src, enc); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("%#v", enc)
}

func TestMergeDesc(t *testing.T) {
	f := strings.NewReader(orig)
	var err error
	if err != nil {
		t.Fatal(err)
	}
	digest := func(s string) []byte {
		m, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	src := &SenderSrcFile{
		SrcFile: SrcFile{
			Path:  "",
			Uid:   0,
			Gid:   0,
			Mode:  0,
			Size:  int64(len(orig)),
			Mtime: time.Time{},
		},
		dst: SenderDstFile{
			DstFile: DstFile{
				ID:        0,
				ChunkSize: 8,
				Size:      1,
			},
			chunks: map[uint32]SenderChunk{
				0x071c019d: {
					id: 0,
					Chunk: Chunk{
						Rsum: 0x071c019d,
						Sum:  digest("2e9ec317e197819358fbc43afca7d837"),
					},
				},
			},
		},
	}
	enc := &mergeDscEnc{}
	if err = sendMergeDescs(f, 22, src, enc); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("%#v", enc)
}

// || + 	&main.mergeDscEnc{
// || + 		main.ChunkType(0), main.MergeReuse{ChunkID: 2, NrChunks: 3}, main.ChunkType(0),
// || + 		main.MergeReuse{ChunkID: 8, NrChunks: 1, Off: 12}, main.ChunkType(1),
// || + 		main.MergeBlob{Off: 16},
// || + 	},
// ||   )
func TestDescEnc(t *testing.T) {
	newBring := func(blockSize int) *Bring {
		var buf bytes.Buffer
		ret := NewBring(&buf, blockSize)
		return &ret
	}
	sendBlob := func(blobSize int64) func(*descEncoder) {
		return func(d *descEncoder) {
			d.sendBlob()
			d.off += blobSize
		}
	}
	sendReuse := func(id int) func(*descEncoder) {
		return func(d *descEncoder) {
			d.sendReuse(id)
		}
	}
	flush := func() func(*descEncoder) { return func(d *descEncoder) { d.flush() } }
	var tests = []struct {
		in    descEncoder
		calls []func(*descEncoder)
		want  *mergeDscEnc
	}{
		{
			in: descEncoder{
				enc:           &mergeDscEnc{},
				r:             newBring(4),
				bsize:         4,
				lastBlockID:   0,
				lastBlockSize: 4,
			},
			calls: []func(d *descEncoder){
				sendReuse(2),
				sendReuse(3),
				sendReuse(4),
				sendReuse(8),
				sendBlob(7),
				sendBlob(5),
			},
			want: &mergeDscEnc{
				ChunkType(0), MergeReuse{ChunkID: 2, NrChunks: 3},
				ChunkType(0), MergeReuse{ChunkID: 8, NrChunks: 1, Off: 12},
				ChunkType(1), MergeBlob{Off: 16},
				ChunkType(1), MergeBlob{Off: 23},
			},
		},
		{
			in: descEncoder{
				enc:           &mergeDscEnc{},
				r:             newBring(8),
				bsize:         8,
				lastBlockID:   3,
				lastBlockSize: 5,
			},
			calls: []func(d *descEncoder){
				sendReuse(1),
				sendReuse(2),
				sendReuse(3),
				sendReuse(5),
				sendBlob(6),
				sendReuse(7),
				flush(),
			},
			want: &mergeDscEnc{
				ChunkType(0), MergeReuse{ChunkID: 1, NrChunks: 3},
				ChunkType(0), MergeReuse{ChunkID: 5, NrChunks: 1, Off: 21},
				ChunkType(1), MergeBlob{Off: 29},
				ChunkType(0), MergeReuse{ChunkID: 7, NrChunks: 1, Off: 35},
			},
		},
		{
			in: descEncoder{
				enc:           &mergeDscEnc{},
				r:             newBring(8),
				bsize:         8,
				lastBlockID:   5,
				lastBlockSize: 5,
			},
			calls: []func(d *descEncoder){
				sendBlob(3),
				sendReuse(0),
				sendReuse(1),
				sendBlob(5),
				sendBlob(8),
				sendReuse(5),
				sendBlob(2),
				flush(),
			},
			want: &mergeDscEnc{
				ChunkType(1), MergeBlob{Off: 0},
				ChunkType(0), MergeReuse{ChunkID: 0, NrChunks: 2, Off: 3},
				ChunkType(1), MergeBlob{Off: 19},
				ChunkType(1), MergeBlob{Off: 24},
				// last block is 5 bytes long
				ChunkType(0), MergeReuse{ChunkID: 5, NrChunks: 1, Off: 32},
				ChunkType(1), MergeBlob{Off: 37},
			},
		},
	}
	for _, tt := range tests {
		for _, fn := range tt.calls {
			fn(&tt.in)
		}
		if diff := cmp.Diff(tt.in.enc, tt.want); diff != "" {
			t.Fatalf("mismatch (-got, +want):\n%s", diff)
		}
	}
}

func TestLastChunkSize(t *testing.T) {
	var tests = []struct {
		in   DstFile
		want int64
	}{
		{DstFile{ChunkSize: 8, Size: 18}, 2},
		{DstFile{ChunkSize: 8, Size: 15}, 7},
		{DstFile{ChunkSize: 7, Size: 15}, 1},
	}
	for _, tt := range tests {
		if got := tt.in.LastChunkSize(); got != tt.want {
			t.Errorf("LastChunkSize(...) = %d, want %d", got, tt.want)
		}
	}
}

// x x x | x x x | x _ _ |
func TestHowMany(t *testing.T) {
	hm := func(x, y int) int {
		return ((x) + ((y) - 1)) / (y)
	}
	r := hm(5, 3)
	if r != 2 {
		t.Errorf("r: %d", r)
	}
}

// func TestRollingReader(t *testing.T) {
// 	var tt = []struct {
// 		in     string
// 		window int
// 		want   []string
// 	}{
// 		{
// 			"abcdefghij",
// 			4,
// 			[]string{
// 				"abcd", "bcde", "cdef", "defg",
// 				"efgh", "fghi", "ghij",
// 			},
// 		},
// 		{
// 			"Plan 9 from outer space",
// 			4,
// 			[]string{
// 				"Plan", "lan ", "an 9", "n 9 ", " 9 f",
// 				"9 fr", " fro", "from", "rom ", "om o",
// 				"m ou", " out", "oute", "uter", "ter ",
// 				"er s", "r sp", " spa", "spac", "pace",
// 			},
// 		},
// 		{
// 			"xyz",
// 			4,
// 			[]string{"xyz"},
// 		},
// 	}
// 	read := func(s string, w int) ([]string, error) {
// 		rr, err := NewRollingReader(bufio.NewReader(strings.NewReader(s)), w)
// 		if err != nil {
// 			return nil, fmt.Errorf("wtf: %w", err)
// 		}
// 		var sl []string
// 		buf := make([]byte, w)
// 		for {
// 			n, err := rr.Read(buf)
// 			if err != nil {
// 				return sl, err
// 			}
// 			sl = append(sl, string(buf[:n]))
// 		}
// 	}
// 	for _, tc := range tt {
// 		got, err := read(tc.in, tc.window)
// 		if err != nil && err != io.EOF {
// 			t.Fatalf("failed: %v", err)
// 		}
// 		if !reflect.DeepEqual(got, tc.want) {
// 			t.Errorf("in: %q, got: %q, want: %q", tc.in, got, tc.want)
// 		}

// 	}
// }

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
