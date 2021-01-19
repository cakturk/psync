package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/chmduquesne/rollinghash/adler32"
)

type Encoder interface {
	Encode(e interface{}) error
	Write(p []byte) (n int, err error)
}

type Decoder interface {
	Read(p []byte) (n int, err error)
	Decode(e interface{}) error
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

type BlockType byte

const (
	RemoteBlockType BlockType = iota
	LocalBlockType
	FileSum
)

func (c BlockType) String() string {
	switch c {
	case RemoteBlockType:
		return "RemoteBlockType"
	case LocalBlockType:
		return "LocalBlockType"
	case FileSum:
		return "FileSum"
	default:
		return "Unknown block type"
	}
}

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

type DstFile struct {
	ID        int
	ChunkSize int
	Size      int64 // 0 means this is a new file
}

func (b *DstFile) NumChunks() int {
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

func main() {
	s := []byte("The quick brown fox jumps over the lazy dog")
	h := adler32.New()
	if _, err := h.Write(s[:16]); err != nil {
		log.Fatal(err)
	}
	for _, v := range s[16:] {
		// fmt.Printf("sum: %x\n", h.Sum32())
		h.Roll(v)
	}
	l, err := genSrcFileList("/tmp/sil/seki")
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	for _, v := range l {
		fmt.Printf("entry: %v\n", v)
	}
}
