package main

import (
	"bufio"
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

type MergeType byte

const (
	New MergeType = iota
	Partial
)

type MergeDesc struct {
	ID        int
	Typ       MergeType
	TotalSize int64
}

type ChunkType byte

const (
	ReuseExisting ChunkType = iota
	Blob
)

func (c *ChunkType) String() string {
	switch *c {
	case ReuseExisting:
		return "ReuseExisting"
	case Blob:
		return "ReuseExisting"
	default:
		return "Unknown chunk ID"
	}
}

type MergeReuse struct {
	ChunkID  int
	NrChunks int
	Off      int64
}

type MergeBlob struct {
	Size, Off int64
}

var errShortRead = errors.New("unexpected EOF")

//
// x x x x x x x x x x x x x x x x x x x x x x
//       |         0     1
func sendMergeDescs(r io.ReadSeeker, id int, e *SrcFile, enc Encoder) error {
	if e.base.Size == 0 {
		enc.Encode(MergeDesc{ID: id, Typ: New, TotalSize: e.Size})
		_, err := io.Copy(enc, r)
		return err
	}
	var rr Ring
	b := bufio.NewReader(r)
	mh := md5.New()
	rh := adler32.New()
	// write initial window
	if n, err := io.CopyN(rh, io.TeeReader(b, &rr), int64(e.base.ChunkSize)); err != nil {
		if err != io.EOF {
			return err
		}
		if n == 0 {
			return errShortRead
		}
	}
	var (
		// err       error
		chunkOff     int64
		chunkID      int
		prevChunkID  int
		firstChunkID = -1
		chunkSize    = int64(e.base.ChunkSize)
	)
	enc.Encode(MergeDesc{ID: id, Typ: Partial})
	chunkOff += chunkSize
	for {
		var (
			c   byte
			err error
		)
		goto Check
	LoopStart:
		chunkOff += chunkSize
		c, err = b.ReadByte()
		if err != nil {
			log.Printf("sendDirections: c: %d, %v", c, err)
			if err == io.EOF {
				break
			}
			return err
		}
		rh.Roll(c)
		rr.WriteByte(c)
	Check:
		prevChunkID = chunkID
		ch, ok := e.base.chunks[rh.Sum32()]
		chunkID = ch.id
		if ok {
			// Check for false positive adler32 matches
			mh.Reset()
			io.CopyN(mh, &rr, chunkSize)
			if bytes.Equal(mh.Sum(nil), ch.Sum) {
				if firstChunkID >= 0 && chunkID-prevChunkID > 1 {
					enc.Encode(ReuseExisting)
					enc.Encode(MergeReuse{
						ChunkID:  firstChunkID,
						NrChunks: prevChunkID + 1 - firstChunkID,
						Off:      chunkOff,
					})
					firstChunkID = -1
				}
				if firstChunkID < 0 {
					firstChunkID = ch.id
				}
				_, err = r.Seek(chunkSize, io.SeekCurrent)
				if err != nil {
					return err
				}
				b.Reset(r)
				continue
			}
		}
		if firstChunkID >= 0 {
			enc.Encode(ReuseExisting)
			enc.Encode(MergeReuse{
				ChunkID:  firstChunkID,
				NrChunks: prevChunkID + 1 - firstChunkID,
				Off:      chunkOff,
			})
			firstChunkID = -1
		}
		enc.Encode(Blob)
		enc.Encode(MergeBlob{
			Size: chunkSize,
			Off:  chunkOff,
		})
		_, err = io.CopyN(enc, &rr, chunkSize)
		if err != nil {
			return err
		}
		n, err := io.CopyN(rh, io.TeeReader(b, &rr), chunkSize)
		if err != nil {
			if err != io.EOF {
				return err
			}
			if n == 0 {
				return errShortRead
			}
		}
		if err := rr.Discard(int(n)); err != nil {
			return err
		}
		goto LoopStart
	}
	if firstChunkID >= 0 {
		enc.Encode(ReuseExisting)
		enc.Encode(MergeReuse{
			ChunkID:  firstChunkID,
			NrChunks: chunkID - firstChunkID,
			Off:      chunkOff,
		})
	}
	return nil
}

//
// x x x x x x x x x x x x x x x x x x x x x x
//         |       0     1
// TODO: calc merge offsets, coalesce concecutive reusable blocks into single
// merge descriptor.
func sendMergeDescs2(r io.ReadSeeker, id int, e *SrcFile, enc Encoder) error {
	if e.base.Size == 0 {
		enc.Encode(MergeDesc{ID: id, Typ: New, TotalSize: e.Size})
		_, err := io.Copy(enc, r)
		return err
	}
	chunkSize := int64(e.base.ChunkSize)
	cr := NewBring(r, int(chunkSize))
	rh := adler32.New()
	mh := md5.New()
	var err error
	de := descEncoder{
		enc:       enc,
		r:         &cr,
		blockSize: chunkSize,
	}
	enc.Encode(MergeDesc{ID: id, Typ: Partial})
	log.Printf("chunkSize: %d", chunkSize)
Outer:
	for err == nil {
		var n int64
		// fill in the buffer
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
		ch, ok := e.base.chunks[rh.Sum32()]
		if ok {
			log.Println("wow I feel good!")
			io.CopyN(mh, cr.Tail(), chunkSize)
			if bytes.Equal(mh.Sum(nil), ch.Sum) {
				de.sendReuse(ch.id)
				continue
			}
			log.Println("but not sooo good")
		}
		for i := int64(0); i < chunkSize; i++ {
			c, err := cr.ReadByte()
			log.Printf("%c", c)
			if err != nil {
				if err == io.EOF {
					log.Printf("2 break: %d", cr.HeadLen())
					break Outer
				}
				log.Printf("3 break: %d", cr.HeadLen())
				return err
			}
			rh.Roll(c)
			ch, ok = e.base.chunks[rh.Sum32()]
			if !ok {
				continue
			}
			mh.Reset()
			io.CopyN(mh, cr.Tail(), chunkSize)
			if bytes.Equal(mh.Sum(nil), ch.Sum) {
				// block matched, send head bytes at first
				if i > 0 {
					err = de.sendBlob()
					if err != nil {
						log.Printf("4 break: %d", cr.HeadLen())
						return err
					}
				}
				de.sendReuse(ch.id)
				continue Outer
			}
		}
		err = de.sendBlob()
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
	err = de.flush()
	if err != nil {
		log.Printf("6 break: %d", cr.HeadLen())
		return err
	}
	log.Printf("prologue point: head: %q, tail: %q", cr.HeadPeek(), cr.TailPeek())
	return nil
}

type descEncoder struct {
	enc            Encoder
	r              *Bring
	blockSize, off int64

	// use offsetting to make the zero value useful,
	// so every time we use this variable we need
	// to subtract by 1 (offset).
	previousID int
	firstID    int
}

// TODO: Occasionally tries to send 1-byte blobs.
func (d *descEncoder) sendBlob() error {
	if _, ok := d.prevID(); ok {
		err := d.flushReuseChunks()
		if err != nil {
			return err
		}
	}
	err := d.enc.Encode(Blob)
	if err != nil {
		return err
	}
	err = d.enc.Encode(MergeBlob{
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

func (d *descEncoder) prevID() (id int, set bool) {
	id = d.previousID - 1
	if id >= 0 {
		set = true
	}
	return
}

func (d *descEncoder) setPrevID(id int) { d.previousID = id + 1 }
func (d *descEncoder) resetPrevID()     { d.setPrevID(-1) }

func (d *descEncoder) sendReuse(id int) error {
	d.r.Skip(int(d.blockSize))
	prevID, set := d.prevID()
	if !set {
		d.setPrevID(id)
		d.firstID = id
		return nil
	}
	if id-prevID != 1 {
		if err := d.flushReuseChunks(); err != nil {
			return err
		}
		d.firstID = id
	}
	d.setPrevID(id)
	return nil
}

func (d *descEncoder) flushReuseChunks() error {
	prevID, ok := d.prevID()
	if !ok {
		return nil
	}
	numChunks := prevID - d.firstID + 1
	err := d.enc.Encode(ReuseExisting)
	if err != nil {
		return nil
	}
	err = d.enc.Encode(MergeReuse{
		ChunkID:  d.firstID,
		NrChunks: numChunks,
		Off:      d.off,
	})
	d.off += d.blockSize * int64(numChunks)
	d.resetPrevID()
	return err
}

func (d *descEncoder) flush() error {
	err := d.flushReuseChunks()
	if err != nil {
		return err
	}
	if d.r.BufferedLen() <= 0 {
		return nil
	}
	err = d.enc.Encode(Blob)
	if err != nil {
		return err
	}
	err = d.enc.Encode(MergeBlob{
		Size: d.r.BufferedLen(),
		Off:  d.off,
	})
	if err != nil {
		return err
	}
	n, err := io.Copy(d.enc, d.r.Buffered())
	d.off += n
	return err
	panic("flush not implemented")
}

type Sender struct {
	r        io.ReadWriter
	enc      Encoder
	root     string
	srcFiles []SrcFile
}

func (s *Sender) sendDirections(id int, e *SrcFile) error {
	if e.Size == 0 {
		return nil
	}
	f, err := os.Open(filepath.Join(s.root, e.Path))
	if err != nil {
		return err
	}
	defer f.Close()
	return sendMergeDescs(f, id, e, s.enc)
}

type SrcFile struct {
	Path     string
	Uid, Gid int
	Mode     os.FileMode
	Size     int64
	Mtime    time.Time

	// following fields are not serialized
	chunkSize int // used by receiver only

	base DstFile // used by sender only
}

type ChunkSrc struct {
	id   int // Chunk ID (index of chunk)
	size int
	Chunk
}

type DstFile struct {
	ID        int
	ChunkSize int
	Size      int64 // 0 means this is a new file

	chunks map[uint32]ChunkSrc // used by sender
}

func (b *DstFile) NumChunks() int {
	return int((b.Size + (int64(b.ChunkSize) - 1)) / int64(b.ChunkSize))
}

type Chunk struct {
	Rsum uint32
	Sum  []byte
}

func (c *Chunk) String() string {
	return fmt.Sprintf("Rsum: %08x, Sum: %s", c.Rsum, hex.EncodeToString(c.Sum))
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
		if err := enc.Encode(Chunk{
			Rsum: rol.Sum32(),
			Sum:  sum.Sum(nil),
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

func SendDstFiles(root string, chunkSize int, list []SrcFile, enc Encoder) error {
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
	l, _ := GenSyncList("/tmp/sil/seki")
	for _, v := range l {
		fmt.Printf("entry: %v\n", v)
	}
}
