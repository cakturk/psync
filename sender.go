package psync

import (
	"bytes"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/chmduquesne/rollinghash/adler32"
)

// SenderSrcFile is a convenience type to represent SrcFile
// info in sender side
type SenderSrcFile struct {
	SrcFile

	// used by sender only. This field filled with the
	// information received from sender.
	dst SenderDstFile
}

// SenderBlockSum is a convenience type to represent Chunk
// info in sender side
type SenderBlockSum struct {
	id int // Chunk ID (index of chunk)
	BlockSum
}

type SenderDstFile struct {
	DstFile

	// map key is adler32 hash of block
	sums map[uint32]SenderBlockSum // used by sender
}

type Sender struct {
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

//
// x x x x x x x x x x x x x x x x x x x x x x
//         |       0     1
// TODO: calc merge offsets, coalesce concecutive blocks into single
// merge descriptor.
func sendBlockDescs(r io.Reader, id int, e *SenderSrcFile, enc Encoder) error {
	if e.dst.Type == DstFileIdentical {
		return nil
	}
	if e.dst.Type == DstFileNotExist {
		enc.Encode(FileDesc{ID: id, Typ: NewFile, TotalSize: e.Size})
		_, err := io.Copy(enc, r)
		return err
	}
	chunkSize := int64(e.dst.ChunkSize)
	rh := adler32.New()
	mh := md5.New()
	sum := md5.New()
	r = io.TeeReader(r, sum)
	cr := NewBring(r, int(chunkSize))
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
	err = enc.Encode(FileSum)
	if err != nil {
		return err
	}
	err = enc.Encode(sum.Sum(nil))
	if err != nil {
		return err
	}
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

func GenSrcFileList(root string) ([]SenderSrcFile, error) {
	var list []SenderSrcFile
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		list = append(list, SenderSrcFile{
			SrcFile: SrcFile{
				Path:  rel,
				Uid:   int(info.Sys().(*syscall.Stat_t).Uid),
				Gid:   int(info.Sys().(*syscall.Stat_t).Gid),
				Mode:  info.Mode(),
				Size:  info.Size(),
				Mtime: info.ModTime(),
			},
		})
		// fmt.Println(rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return list, nil
}

// Sender protocol is more or less as described below:
// - send the file list header (FileListHdr),
// - send as many source files submitted in the previous item,
// - and then read the same number of target files from the receiver side
// - remember, each target file contains (*DstFile).NumChunks() number of
//   blocks after it.
func SendSrcFileList(enc Encoder, list []SenderSrcFile) error {
	hdr := FileListHdr{
		NumFiles: len(list),
		Type:     SenderFileList,
	}
	err := enc.Encode(&hdr)
	if err != nil {
		return fmt.Errorf("sending src list header failed: %w", err)
	}
	for i := range list {
		err := enc.Encode(&list[i].SrcFile)
		if err != nil {
			return fmt.Errorf("sending src list failed: %w", err)
		}
	}
	return nil
}

func recvDstFileList(dec Decoder, list []SenderSrcFile) error {
	var hdr FileListHdr
	err := dec.Decode(&hdr)
	if err != nil {
		return fmt.Errorf("failed to recv dst header: %w", err)
	}
	if hdr.Type != ReceiverFileList {
		return fmt.Errorf("sender: invalid header type: %v", hdr.Type)
	}
	for i := 0; i < hdr.NumFiles; i++ {
		err := dec.Decode(&list[i].dst.DstFile)
		if err != nil {
			return fmt.Errorf("failed to recv dst list: %w", err)
		}
		// sanity check
		if id := list[i].dst.ID; id != i {
			return fmt.Errorf("dst file invalid ID got: %d, want: %d", id, i)
		}
		dst := &list[i].dst
		dst.sums = make(map[uint32]SenderBlockSum)
		nrBlocks := dst.NumChunks()
		for j := 0; j < nrBlocks; j++ {
			var bs SenderBlockSum
			err := dec.Decode(&bs.BlockSum)
			if err != nil {
				return fmt.Errorf("recving block sum failed: %w", err)
			}
			bs.id = j
			if _, ok := dst.sums[bs.Rsum]; ok {
				return errors.New("duplicate block received")
			}
			dst.sums[bs.Rsum] = bs
		}
	}
	return nil
}
