package psync

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"
)

//go:generate stringer -type=FileType,FileListType,DstFileType,BlockType -output types_string.go

type Encoder interface {
	Encode(e interface{}) error
}

type EncodeWriter interface {
	Encoder
	io.Writer
}

type Decoder interface {
	Decode(e interface{}) error
}

type DecodeReader interface {
	Decoder
	io.Reader
}

var ProtoMagic = [...]byte{'p', 's', 'y', 'n'}

// Handshake represents a 8-byte protocol header
type Handshake struct {
	Magic      [4]byte
	Version    uint16
	WireFormat byte
	Flags      byte
}

const (
	// Wire format encoders
	WireFormatGob = iota

	// Supported flags
	CompressGzip = 1 << 0
)

func NewHandshake(version uint16, wireFormat, flags byte) *Handshake {
	h := &Handshake{
		Magic:      [4]byte{'p', 's', 'y', 'n'},
		Version:    version,
		WireFormat: wireFormat,
		Flags:      flags,
	}
	return h
}

func (h *Handshake) WriteTo(w io.Writer) (int64, error) {
	var b bytes.Buffer
	_, err := b.Write(h.Magic[:])
	if err != nil {
		return 0, err
	}
	var s [2]byte
	binary.BigEndian.PutUint16(s[:], h.Version)
	_, err = b.Write(s[:])
	if err != nil {
		return 0, err
	}
	err = b.WriteByte(h.WireFormat)
	if err != nil {
		return 0, err
	}
	err = b.WriteByte(h.Flags)
	if err != nil {
		return 0, err
	}
	_, err = b.WriteTo(w)
	if err != nil {
		return 0, err
	}
	return 8, nil
}

// Valid determines whether this is a valid Handshake header. Note that
// this function does nothing with the protocol version, you need to
// explicitly check the required protocol version yourself.
func (h *Handshake) Valid() bool { return bytes.Equal(ProtoMagic[:], h.Magic[:]) }

// ReadHandshake tries to decode bytes read from r into a Handshake structure.
// You are advised to set read time-outs in order not to block your program
// indefinitely.
// We'll ensure that we get Handshake from the remote peer in a timely
// manner. If they don't respond within handshakeReadDeadline amount
// of time, then we'll close the connection.
func ReadHandshake(r io.Reader) (Handshake, error) {
	var h Handshake
	var p [8]byte
	_, err := io.ReadFull(r, p[:])
	if err != nil {
		return Handshake{}, err
	}
	copy(h.Magic[:], p[:4])
	h.Version = binary.BigEndian.Uint16(p[4:6])
	h.WireFormat = p[6]
	h.Flags = p[7]
	return h, nil
}

type FileType byte

const (
	NewFile FileType = iota
	PartialFile
)

type FileDesc struct {
	ID        int
	Typ       FileType
	TotalSize int64
}

type FileListType byte

const (
	InvalidFileListType FileListType = iota
	SenderFileList
	ReceiverFileList
)

type FileListHdr struct {
	NumFiles int
	Type     FileListType
}

type BlockType byte

const (
	RemoteBlockType BlockType = iota
	LocalBlockType
	FileSum
)

type RemoteBlock struct {
	ChunkID  int
	NrChunks int

	// offset of existing block in the target file
	// read offset in the similar file
	Off int64
}

type LocalBlock struct {
	Size int64

	// offset of data block in the newly created file
	// write offset in the target file
	Off int64
}

type SrcFile struct {
	Path     string
	Uid, Gid int
	Mode     os.FileMode
	Size     int64
	Mtime    time.Time
}

type DstFileType int

const (
	DstFileSimilar DstFileType = iota
	DstFileIdentical
	DstFileNotExist
)

type DstFile struct {
	ID        int
	ChunkSize int

	// 0 means this is a new file.
	// In this context, -1 means modification times and sizes of the
	// two files do not differ, which means we consider them as two
	// identical files.
	Size int64

	Type DstFileType
}

func (b *DstFile) NumChunks() int {
	if b.ChunkSize <= 0 || b.Size <= 0 {
		return 0
	}
	return int((b.Size + (int64(b.ChunkSize) - 1)) / int64(b.ChunkSize))
}

func (b *DstFile) LastChunkID() int     { return b.NumChunks() - 1 }
func (b *DstFile) LastChunkSize() int64 { return b.Size % int64(b.ChunkSize) }

type BlockSum struct {
	Rsum uint32
	Csum []byte
}

func (c *BlockSum) String() string {
	return fmt.Sprintf("Rsum: %08x, Sum: %s", c.Rsum, hex.EncodeToString(c.Csum))
}

// func main() {
// 	s := []byte("The quick brown fox jumps over the lazy dog")
// 	h := adler32.New()
// 	if _, err := h.Write(s[:16]); err != nil {
// 		log.Fatal(err)
// 	}
// 	for _, v := range s[16:] {
// 		// fmt.Printf("sum: %x\n", h.Sum32())
// 		h.Roll(v)
// 	}
// 	l, err := genSrcFileList("/tmp/sil/seki")
// 	if err != nil {
// 		fmt.Printf("%v\n", err)
// 	}
// 	for _, v := range l {
// 		fmt.Printf("entry: %v\n", v)
// 	}
// }
