package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	stdadler32 "hash/adler32"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/chmduquesne/rollinghash/adler32"
)

type Encoder interface {
	Encode(e interface{}) error
	Write(p []byte) (n int, err error)
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
)

func (c BlockType) String() string {
	switch c {
	case RemoteBlockType:
		return "LocalBlockType"
	case LocalBlockType:
		return "RemoteBlockType"
	default:
		return "Unknown block type"
	}
}

type RemoteBlock struct {
	ChunkID  int
	NrChunks int
	Off      int64
}

type LocalBlock struct {
	Size, Off int64
}

var errShortRead = errors.New("unexpected EOF")

//
// x x x x x x x x x x x x x x x x x x x x x x
//         |       0     1
// TODO: calc merge offsets, coalesce concecutive blocks into single
// merge descriptor.
func sendBlockDescs(r io.ReadSeeker, id int, e *SenderSrcFile, enc Encoder) error {
	if e.dst.Size == 0 {
		enc.Encode(FileDesc{ID: id, Typ: NewFile, TotalSize: e.Size})
		_, err := io.Copy(enc, r)
		return err
	}
	chunkSize := int64(e.dst.ChunkSize)
	cr := NewBring(r, int(chunkSize))
	rh := adler32.New()
	mh := md5.New()
	var err error
	ben := blockEncoder{
		enc:           enc,
		r:             &cr,
		bsize:         chunkSize,
		lastBlockID:   e.dst.LastChunkID(),
		lastBlockSize: e.dst.LastChunkSize(),
	}
	enc.Encode(FileDesc{ID: id, Typ: PartialFile})
	log.Printf("chunkSize: %d", chunkSize)
Outer:
	for {
		var n int64
		// fill in the buffer
		rh.Reset()
		n, err = io.CopyN(rh, &cr, chunkSize)
		if err != nil {
			if err != io.EOF {
				log.Printf("0 break: %d", cr.HeadLen())
				return err
			}
			if n == 0 {
				log.Printf("1 break: %d", cr.HeadLen())
				break
			}
		}
		log.Printf("head0: %q, tail: %q, adler: %x", cr.HeadPeek(), cr.TailPeek(), rh.Sum32())
		ch, ok := e.dst.sums[rh.Sum32()]
		if ok {
			log.Println("wow I feel good!")
			mh.Reset()
			io.CopyN(mh, cr.Tail(), chunkSize)
			if bytes.Equal(mh.Sum(nil), ch.Csum) {
				ben.sendRemoteBlock(ch.id)
				continue
			}
			log.Println("but not sooo good")
		}
		for i := int64(0); i < chunkSize; i++ {
			c, err := cr.ReadByte()
			if err != nil {
				if err == io.EOF {
					log.Printf("2 break: %d", cr.HeadLen())
					break Outer
				}
				log.Printf("3 break: %d", cr.HeadLen())
				return err
			}
			rh.Roll(c)
			log.Printf("%q, adler: 0x%x", c, rh.Sum32())
			ch, ok = e.dst.sums[rh.Sum32()]
			if !ok {
				continue
			}
			mh.Reset()
			io.CopyN(mh, cr.Tail(), chunkSize)
			if bytes.Equal(mh.Sum(nil), ch.Csum) {
				// block matched, send head bytes at first
				if i > 0 {
					err = ben.sendLocalBlock()
					if err != nil {
						log.Printf("4 break: %d", cr.HeadLen())
						return err
					}
				}
				ben.sendRemoteBlock(ch.id)
				continue Outer
			}
		}
		err = ben.sendLocalBlock()
		// enc.Encode(Blob)
		// log.Printf(
		// 	"headlen: %d, head: %q, tail: %q",
		// 	cr.HeadLen(), cr.buf.Bytes()[:cr.buf.Len()-cr.blockSize],
		// 	cr.buf.Bytes()[cr.buf.Len()-cr.blockSize:],
		// )
		log.Printf("head: %q, tail: %q", cr.HeadPeek(), cr.TailPeek())
		// log.Printf("headlenPost: %d, %q, written: %d", cr.HeadLen(), cr.buf.Bytes()[:cr.buf.Len()], n)
		if err != nil {
			log.Printf("5 break: %d", cr.HeadLen())
			return err
		}
	}
	err = ben.flush()
	if err != nil {
		log.Printf("6 break: %d", cr.HeadLen())
		return err
	}
	log.Printf("prologue point: head: %q, tail: %q", cr.HeadPeek(), cr.TailPeek())
	return nil
}

type blockEncoder struct {
	enc        Encoder
	r          *Bring
	bsize, off int64

	lastBlockID   int
	lastBlockSize int64

	// use offsetting to make the zero value useful,
	// so every time we use this variable we need
	// to subtract by 1 (offset).
	previousID      int
	firstID         int
	contiguousBsize int64
}

func (d *blockEncoder) findBlockSize(id int) int64 {
	if id == d.lastBlockID {
		return d.lastBlockSize
	}
	return d.bsize
}

// TODO: Occasionally tries to send 1-byte blobs.
func (d *blockEncoder) sendLocalBlock() error {
	if _, ok := d.prevID(); ok {
		err := d.flushReuseChunks()
		if err != nil {
			return err
		}
	}
	err := d.enc.Encode(LocalBlockType)
	if err != nil {
		return err
	}
	err = d.enc.Encode(LocalBlock{
		Size: d.r.HeadLen(),
		Off:  d.off,
	})
	if err != nil {
		return err
	}
	n, err := io.Copy(d.enc, d.r.Head())
	d.off += n
	return err
}

func (d *blockEncoder) prevID() (id int, set bool) {
	id = d.previousID - 1
	if id >= 0 {
		set = true
	}
	return
}

func (d *blockEncoder) setPrevID(id int) { d.previousID = id + 1 }
func (d *blockEncoder) resetPrevID()     { d.setPrevID(-1) }

func (d *blockEncoder) sendRemoteBlock(id int) error {
	bsize := d.findBlockSize(id)
	d.r.Skip(int(bsize))
	prevID, set := d.prevID()
	if !set {
		d.setPrevID(id)
		d.firstID = id
		d.contiguousBsize = bsize
		return nil
	}
	if id-prevID != 1 {
		if err := d.flushReuseChunks(); err != nil {
			return err
		}
		d.firstID = id
	}
	d.contiguousBsize += bsize
	d.setPrevID(id)
	return nil
}

func (d *blockEncoder) flushReuseChunks() error {
	prevID, ok := d.prevID()
	if !ok {
		return nil
	}
	numChunks := prevID - d.firstID + 1
	err := d.enc.Encode(RemoteBlockType)
	if err != nil {
		return nil
	}
	err = d.enc.Encode(RemoteBlock{
		ChunkID:  d.firstID,
		NrChunks: numChunks,
		Off:      d.off,
	})
	d.off += d.contiguousBsize
	d.contiguousBsize = 0
	d.resetPrevID()
	return err
}

func (d *blockEncoder) flush() error {
	err := d.flushReuseChunks()
	if err != nil {
		return err
	}
	if d.r.BufferedLen() <= 0 {
		return nil
	}
	err = d.enc.Encode(LocalBlockType)
	if err != nil {
		return err
	}
	err = d.enc.Encode(LocalBlock{
		Size: d.r.BufferedLen(),
		Off:  d.off,
	})
	if err != nil {
		return err
	}
	n, err := io.Copy(d.enc, d.r.Buffered())
	d.off += n
	return err
}

type Sender struct {
	r        io.ReadWriter
	enc      Encoder
	root     string
	srcFiles []SenderSrcFile
}

func (s *Sender) sendBlockDescs(id int, e *SenderSrcFile) error {
	if e.Size == 0 {
		return nil
	}
	f, err := os.Open(filepath.Join(s.root, e.Path))
	if err != nil {
		return err
	}
	defer f.Close()
	return sendBlockDescs(f, id, e, s.enc)
}

type SrcFile struct {
	Path     string
	Uid, Gid int
	Mode     os.FileMode
	Size     int64
	Mtime    time.Time
}

type ReceiverSrcFile struct {
	SrcFile

	// following fields are not serialized
	chunkSize int // used by receiver only
}

// SenderSrcFile is a convenience type to represent SrcFile
// info in sender side
type SenderSrcFile struct {
	SrcFile
	dst SenderDstFile // used by sender only
}

// SenderBlockSum is a convenience type to represent Chunk
// info in sender side
type SenderBlockSum struct {
	id int // Chunk ID (index of chunk)
	BlockSum
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

type SenderDstFile struct {
	DstFile
	sums map[uint32]SenderBlockSum // used by sender
}

type BlockSum struct {
	Rsum uint32
	Csum []byte
}

func (c *BlockSum) String() string {
	return fmt.Sprintf("Rsum: %08x, Sum: %s", c.Rsum, hex.EncodeToString(c.Csum))
}

func doChunkFile(r io.Reader, enc Encoder, blkSize int) error {
	sum := md5.New()
	rol := stdadler32.New()
	w := io.MultiWriter(sum, rol)
	var err error
	for err == nil {
		var n int64
		if n, err = io.CopyN(w, r, int64(blkSize)); err != nil {
			if err != io.EOF {
				return err
			}
			if n == 0 {
				break
			}
		}
		if err := enc.Encode(BlockSum{
			Rsum: rol.Sum32(),
			Csum: sum.Sum(nil),
		}); err != nil {
			return err
		}
		rol.Reset()
		sum.Reset()
	}
	return nil
}

func chunkFile(path string, enc Encoder, blockSize int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return doChunkFile(f, enc, blockSize)
}

func SendDstFiles(root string, chunkSize int, list []ReceiverSrcFile, enc Encoder) error {
	for i, v := range list {
		path := filepath.Join(root, v.Path)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if err := enc.Encode(DstFile{ID: i}); err != nil {
					return err
				}
				continue
			}
			return nil
		}
		if info.ModTime() == v.Mtime && info.Size() == v.Size {
			continue
		}
		if err := enc.Encode(DstFile{
			ID:        i,
			ChunkSize: chunkSize,
			Size:      info.Size(),
		}); err != nil {
			return err
		}
		list[i].chunkSize = chunkSize
		if err := chunkFile(path, enc, chunkSize); err != nil {
			return err
		}
	}
	return nil
}

func GenSyncList(root string) ([]SrcFile, error) {
	var list []SrcFile
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		list = append(list, SrcFile{
			Path:  rel,
			Uid:   int(info.Sys().(*syscall.Stat_t).Uid),
			Gid:   int(info.Sys().(*syscall.Stat_t).Gid),
			Mode:  info.Mode(),
			Size:  info.Size(),
			Mtime: info.ModTime(),
		})
		// fmt.Println(rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return list, nil
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
	l, err := GenSyncList("/tmp/sil/seki")
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	for _, v := range l {
		fmt.Printf("entry: %v\n", v)
	}
}
