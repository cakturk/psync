package psync

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"encoding/hex"
	stdadler32 "hash/adler32"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chmduquesne/rollinghash/adler32"
	"github.com/google/go-cmp/cmp"
)

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

type strSliceWriter []string

func (s *strSliceWriter) Write(p []byte) (n int, err error) {
	*s = append(*s, string(p))
	return len(p), nil
}

func digest(t *testing.T, s string) []byte {
	t.Helper()
	m, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("failed to decode string: %q", s)
	}
	return m
}

func TestDoChunkFile(t *testing.T) {
	digest := func(s string) []byte { return digest(t, s) }
	f := strings.NewReader(orig)
	var buf strSliceWriter
	tr := io.TeeReader(f, &buf)
	enc := mergeDscEnc{}
	if err := doChunkFile(tr, &enc, 8); err != nil {
		t.Fatal(err)
	}
	var sums []BlockSum
	for _, v := range enc {
		ch := v.(BlockSum)
		sums = append(sums, ch)
		// t.Errorf("%v", &ch)
	}
	// t.Fatalf("%#v", buf)
	want := []BlockSum{
		{Rsum: 0x071c019d, Csum: digest("2e9ec317e197819358fbc43afca7d837")},
		{Rsum: 0x0a3a0291, Csum: digest("0971ea36560f190d33257a3722f2b08c")},
		{Rsum: 0x0c1402ea, Csum: digest("6f1adba1b07b8042ab76144a2bc98f86")},
		{Rsum: 0x0fb00385, Csum: digest("a70900006e6c6e510d501865a9f65efd")},
		{Rsum: 0x0fc20328, Csum: digest("aa7e6f7af8d9f4ce4bbe37c99645068a")},
		{Rsum: 0x0d790309, Csum: digest("7f75672f0f60125b9d78fc51fd5c3614")},
		{Rsum: 0x0d090302, Csum: digest("008f7a640603fa380ae5fa52eddb1f9f")},
		{Rsum: 0x000b000b, Csum: digest("68b329da9893e34099c7d8ad5cb9c940")},
	}
	if diff := cmp.Diff(want, sums); diff != "" {
		t.Fatalf("doChunkFile() mismatch (-want, +got):\n%s", diff)
	}
}

type mergeDscEnc []interface{}

func (s *mergeDscEnc) Write(p []byte) (n int, err error) {
	// log.Printf("write invoked: %d, %q", len(p), p)
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
	// 59 bytes
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
func TestMergeDesc(t *testing.T) {
	f := strings.NewReader(modified)
	var err error
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte(modified))
	want := &mergeDscEnc{
		FileDesc{ID: 22, Typ: PartialFile, TotalSize: 0},
		RemoteBlockType, RemoteBlock{ChunkID: 0, NrChunks: 3, Off: 0},

		LocalBlockType, LocalBlock{Size: 8, Off: 24},
		// {0x6d, 0x6e, 0x6f, 0x70, 0x2d, 0x6d, 0x6f, 0x64}
		[]byte("mnop-mod"),

		LocalBlockType, LocalBlock{Size: 10, Off: 32},
		// {0x69, 0x66, 0x69, 0x65, 0x64, 0x2d, 0x6c, 0x61, 0xa, 0x50}
		[]byte("ified-la\nP"),

		RemoteBlockType, RemoteBlock{ChunkID: 5, NrChunks: 3, Off: 42},

		FileSum, sum[:],
	}
	digest := func(s string) []byte { return digest(t, s) }
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
				Size:      int64(len(orig)),
			},
			sums: map[uint32]SenderBlockSum{
				0x071c019d: {
					id:       0, // chunk id
					BlockSum: BlockSum{Rsum: 0x071c019d, Csum: digest("2e9ec317e197819358fbc43afca7d837")},
				},
				0x0a3a0291: {
					id:       1, // chunk id
					BlockSum: BlockSum{Rsum: 0x0a3a0291, Csum: digest("0971ea36560f190d33257a3722f2b08c")},
				},
				0x0c1402ea: {
					id:       2, // chunk id
					BlockSum: BlockSum{Rsum: 0x0c1402ea, Csum: digest("6f1adba1b07b8042ab76144a2bc98f86")},
				},
				0x0fb00385: {
					id:       3, // chunk id
					BlockSum: BlockSum{Rsum: 0x0fb00385, Csum: digest("a70900006e6c6e510d501865a9f65efd")},
				},
				0x0fc20328: {
					id:       4, // chunk id
					BlockSum: BlockSum{Rsum: 0x0fc20328, Csum: digest("aa7e6f7af8d9f4ce4bbe37c99645068a")},
				},
				0x0d790309: {
					id:       5, // chunk id
					BlockSum: BlockSum{Rsum: 0x0d790309, Csum: digest("7f75672f0f60125b9d78fc51fd5c3614")},
				},
				0x0d090302: {
					id:       6, // chunk id
					BlockSum: BlockSum{Rsum: 0x0d090302, Csum: digest("008f7a640603fa380ae5fa52eddb1f9f")},
				},
				0x000b000b: {
					id:       7, // chunk id
					BlockSum: BlockSum{Rsum: 0x000b000b, Csum: digest("68b329da9893e34099c7d8ad5cb9c940")},
				},
			},
		},
	}
	enc := &mergeDscEnc{}
	if err = sendBlockDescs(f, 22, src, enc); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, enc); diff != "" {
		t.Fatalf("mismatch (-want, +got):\n%s", diff)
	}
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
	sendLocalBlock := func(blobSize int64) func(*blockEncoder) {
		return func(d *blockEncoder) {
			d.sendLocalBlock()
			d.off += blobSize
		}
	}
	sendRemoteBlock := func(id int) func(*blockEncoder) {
		return func(d *blockEncoder) {
			d.sendRemoteBlock(id)
		}
	}
	flush := func() func(*blockEncoder) { return func(d *blockEncoder) { d.flush() } }
	var tests = []struct {
		in    blockEncoder
		calls []func(*blockEncoder)
		want  *mergeDscEnc
	}{
		{
			in: blockEncoder{
				enc:           &mergeDscEnc{},
				r:             newBring(4),
				bsize:         4,
				lastBlockID:   0,
				lastBlockSize: 4,
			},
			calls: []func(d *blockEncoder){
				sendRemoteBlock(2),
				sendRemoteBlock(3),
				sendRemoteBlock(4),
				sendRemoteBlock(8),
				sendLocalBlock(7),
				sendLocalBlock(5),
			},
			want: &mergeDscEnc{
				RemoteBlockType, RemoteBlock{ChunkID: 2, NrChunks: 3},
				RemoteBlockType, RemoteBlock{ChunkID: 8, NrChunks: 1, Off: 12},
				LocalBlockType, LocalBlock{Off: 16},
				LocalBlockType, LocalBlock{Off: 23},
			},
		},
		{
			in: blockEncoder{
				enc:           &mergeDscEnc{},
				r:             newBring(8),
				bsize:         8,
				lastBlockID:   3,
				lastBlockSize: 5,
			},
			calls: []func(d *blockEncoder){
				sendRemoteBlock(1),
				sendRemoteBlock(2),
				sendRemoteBlock(3),
				sendRemoteBlock(5),
				sendLocalBlock(6),
				sendRemoteBlock(7),
				flush(),
			},
			want: &mergeDscEnc{
				RemoteBlockType, RemoteBlock{ChunkID: 1, NrChunks: 3},
				RemoteBlockType, RemoteBlock{ChunkID: 5, NrChunks: 1, Off: 21},
				LocalBlockType, LocalBlock{Off: 29},
				RemoteBlockType, RemoteBlock{ChunkID: 7, NrChunks: 1, Off: 35},
			},
		},
		{
			in: blockEncoder{
				enc:           &mergeDscEnc{},
				r:             newBring(8),
				bsize:         8,
				lastBlockID:   5,
				lastBlockSize: 5,
			},
			calls: []func(d *blockEncoder){
				sendLocalBlock(3),
				sendRemoteBlock(0),
				sendRemoteBlock(1),
				sendLocalBlock(5),
				sendLocalBlock(8),
				sendRemoteBlock(5),
				sendLocalBlock(2),
				flush(),
			},
			want: &mergeDscEnc{
				LocalBlockType, LocalBlock{Off: 0},
				RemoteBlockType, RemoteBlock{ChunkID: 0, NrChunks: 2, Off: 3},
				LocalBlockType, LocalBlock{Off: 19},
				LocalBlockType, LocalBlock{Off: 24},
				// last block is 5 bytes long
				RemoteBlockType, RemoteBlock{ChunkID: 5, NrChunks: 1, Off: 32},
				LocalBlockType, LocalBlock{Off: 37},
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

func createFakeDecoder(a ...interface{}) Decoder {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	btype := RemoteBlockType
	for _, v := range a {
		switch y := v.(type) {
		case []byte:
			if btype != FileSum {
				_, err := b.Write(y)
				if err != nil {
					panic(err)
				}
				continue
			}
		case BlockType:
			btype = y
		}
		err := enc.Encode(v)
		if err != nil {
			panic(err)
		}
	}
	type fakeDecoder struct {
		*gob.Decoder
		io.Reader
	}
	fk := fakeDecoder{
		Decoder: gob.NewDecoder(&b),
		Reader:  &b,
	}
	return &fk
}

func TestBuilder(t *testing.T) {
	// origBlocks := []string{
	// 	"01234567", "890abcde", "f\nghijkl", "mnopqrst",
	// 	"uvwxyz\np", "lan9From", "BellLabs", "\n",
	// }
	csum := func(s string) []byte {
		sum := md5.Sum([]byte(s))
		return sum[:]
	}
	var tests = []struct {
		rcv  Receiver
		want string
	}{
		{
			rcv: Receiver{
				root: "",
				srcFiles: []ReceiverSrcFile{
					{
						SrcFile: SrcFile{
							Path:  "",
							Mode:  0666,
							Size:  39,
							Mtime: time.Now(),
						},
						chunkSize: 8,
					},
				},
				dec: createFakeDecoder(
					LocalBlockType,
					LocalBlock{
						Size: 10,
						Off:  0,
					},
					[]byte("foobarbazp"),
					RemoteBlockType,
					RemoteBlock{
						ChunkID:  2,
						NrChunks: 2,
						Off:      10,
					},
					RemoteBlockType,
					RemoteBlock{
						ChunkID:  6,
						NrChunks: 1,
						Off:      26,
					},
					LocalBlockType,
					LocalBlock{
						Size: 4,
						Off:  34,
					},
					[]byte("heap"),
					RemoteBlockType,
					RemoteBlock{
						ChunkID:  7,
						NrChunks: 1,
						Off:      38,
					},
					FileSum,
					csum("foobarbazpf\nghijklmnopqrstBellLabsheap\n"),
				),
			},
			want: "foobarbazpf\nghijklmnopqrstBellLabsheap\n",
		},
		{
			rcv: Receiver{
				root: "",
				srcFiles: []ReceiverSrcFile{
					{
						SrcFile: SrcFile{
							Path:  "",
							Mode:  0666,
							Size:  29,
							Mtime: time.Now(),
						},
						chunkSize: 8,
					},
				},
				dec: createFakeDecoder(
					RemoteBlockType,
					RemoteBlock{
						ChunkID:  5,
						NrChunks: 2,
						Off:      0,
					},
					LocalBlockType,
					LocalBlock{
						Size: 4,
						Off:  16,
					},
					[]byte("heap"),
					RemoteBlockType,
					RemoteBlock{
						ChunkID:  0,
						NrChunks: 1,
						Off:      20,
					},
					RemoteBlockType,
					RemoteBlock{
						ChunkID:  7,
						NrChunks: 1,
						Off:      28,
					},
					FileSum,
					csum("lan9FromBellLabsheap01234567\n"),
				),
			},
			want: "lan9FromBellLabsheap01234567\n",
		},
		{
			rcv: Receiver{
				root: "",
				srcFiles: []ReceiverSrcFile{
					{
						SrcFile: SrcFile{
							Path:  "",
							Mode:  0666,
							Size:  8,
							Mtime: time.Now(),
						},
						chunkSize: 8,
					},
				},
				dec: createFakeDecoder(
					LocalBlockType,
					LocalBlock{
						Size: 3,
						Off:  0,
					},
					[]byte("SOH"),
					RemoteBlockType,
					RemoteBlock{
						ChunkID:  7,
						NrChunks: 1,
						Off:      3,
					},
					LocalBlockType,
					LocalBlock{
						Size: 4,
						Off:  4,
					},
					[]byte("sinh"),
					FileSum,
					csum("SOH\nsinh"),
				),
			},
			want: "SOH\nsinh",
		},
	}
	for _, tt := range tests {
		var b bytes.Buffer
		err := tt.rcv.merge(&tt.rcv.srcFiles[0], strings.NewReader(orig), &b)
		if err != nil {
			t.Error(err)
		}
		if got := b.String(); got != tt.want {
			t.Fatalf("merge(...) = %q, want %q", got, tt.want)
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

func TestHandshake(t *testing.T) {
	var b bytes.Buffer
	want := NewHandshake(0x1a2b, WireFormatGob, CompressGzip)
	_, err := want.WriteTo(&b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadHandshake(&b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Fatalf("Handshake.WriteTo(...) = %#v, want %#v", got, want)
	}
}

/*
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
*/
