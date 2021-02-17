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
	Enc  EncodeWriter
	Root string
}

func (s *Sender) SendBlockDescList(files []SenderSrcFile) error {
	log.Printf("sendOneBlockDesc: 0")
	for i := range files {
		sf := &files[i]
		log.Print("sendOneBlockDesc:", sf.Path)
		if sf.dst.Type != DstFileIdentical && !sf.Mode.IsDir() {
			err := s.sendOneBlockDesc(i, sf)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// TODO: Is this id parameter really needed? Maybe we can get it from
// the destination file struct.
func (s *Sender) sendOneBlockDesc(id int, e *SenderSrcFile) error {
	f, err := os.Open(filepath.Join(s.Root, e.Path))
	if err != nil {
		return err
	}
	defer f.Close()
	return sendBlockDescs(f, id, e, s.Enc)
}

//
// x x x x x x x x x x x x x x x x x x x x x x
//         |       0     1
// TODO: calc merge offsets, coalesce concecutive blocks into single
// merge descriptor.
func sendBlockDescs(r io.Reader, id int, e *SenderSrcFile, enc EncodeWriter) error {
	if e.dst.Type == DstFileIdentical {
		return nil
	}
	if e.dst.Type == DstFileNotExist {
		enc.Encode(FileDesc{ID: id, Typ: NewFile, TotalSize: e.Size})
		_, err := io.Copy(enc, r)
		return err
	}
	chunkSize := int64(e.dst.ChunkSize)
	sum := md5.New()
	r = io.TeeReader(r, sum)
	cr := NewBring(r, int(chunkSize))
	var err error
	ben := blockEncoder{
		enc:           enc,
		r:             &cr,
		blockSize:     chunkSize,
		remainder:     e.Size,
		lastBlockID:   e.dst.LastChunkID(),
		lastBlockSize: e.dst.LastChunkSize(),
	}
	enc.Encode(FileDesc{ID: id, Typ: PartialFile})
	log.Printf("chunkSize: %d", chunkSize)
	err = blockDescLoop(&ben, &cr, e)
	if err != nil {
		if !errors.Is(err, errNoSpaceLeft) {
			return err
		}
		log.Printf("run into errNoSpaceLeft")
		if err := ben.flushReuseChunks(); err != nil {
			return err
		}
	}
	log.Printf("prologue point: head: %q, tail: %q", cr.HeadPeek(), cr.TailPeek())
	log.Printf("written: %d bytes, remainder: %d, size: %d", ben.off, ben.remainder, e.Size)
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

func blockDescLoop(ben *blockEncoder, cr *Bring, e *SenderSrcFile) error {
	chunkSize := int64(e.dst.ChunkSize)
	rh := adler32.New()
	mh := md5.New()
	var err error
Outer:
	for {
		var n int64
		// fill in the buffer
		rh.Reset()
		n, err = io.CopyN(rh, cr, chunkSize)
		log.Printf("yukari: %d, %v", n, err)
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
		ch, ok := e.dst.sums[rh.Sum32()]
		if ok {
			mh.Reset()
			io.CopyN(mh, cr.Tail(), chunkSize)
			if bytes.Equal(mh.Sum(nil), ch.Csum) {
				log.Printf("blocks matched0: h: %q, t: %q", cr.HeadPeek(), cr.TailPeek())
				if cr.HeadLen() > 0 {
					err = ben.sendLocalBlock()
					if err != nil {
						log.Printf("sendLocalBlock err0: %q", cr.HeadLen())
						return err
					}
				}
				ben.sendRemoteBlock(ch.id)
				continue
			}
		}
		for i := int64(0); i < chunkSize; i++ {
			c, err := cr.ReadByte()
			if err != nil {
				if err == io.EOF {
					log.Printf("ReadByte reached EOF: %q", cr.HeadPeek())
					break Outer
				}
				return fmt.Errorf("ReadByte: %w", err)
			}
			rh.Roll(c)
			ch, ok = e.dst.sums[rh.Sum32()]
			if !ok {
				continue
			}
			mh.Reset()
			io.CopyN(mh, cr.Tail(), chunkSize)
			if bytes.Equal(mh.Sum(nil), ch.Csum) {
				// block matched, send head bytes at first
				log.Printf("blocks matched1: i: %d, h: %q, t: %q, ", i, cr.HeadPeek(), cr.TailPeek())
				if cr.HeadLen() > 0 {
					err = ben.sendLocalBlock()
					if err != nil {
						log.Printf("sendLocalBlock err1: %q", cr.HeadLen())
						return err
					}
				}
				ben.sendRemoteBlock(ch.id)
				fmt.Println("continue Outer")
				continue Outer
			}
		}
		log.Printf("head alt0: %q, tail: %q", cr.HeadPeek(), cr.TailPeek())
		err = ben.sendLocalBlock()
		if err != nil {
			log.Printf("sendLocalBlock err2: %d, err: %v", cr.HeadLen(), err)
			return err
		}
		log.Printf("head alt1: %q, tail: %q", cr.HeadPeek(), cr.TailPeek())
	}
	err = ben.flush()
	if err != nil {
		log.Printf("flush: %d, err: %v", cr.HeadLen(), err)
		if err != errNoSpaceLeft {
			return err
		}
	}
	return nil
}

type blockEncoder struct {
	enc            EncodeWriter
	r              *Bring
	blockSize, off int64

	remainder int64

	lastBlockID   int
	lastBlockSize int64

	// use offsetting to make the zero value useful,
	// so every time we use this variable we need
	// to subtract by 1 (offset).
	previousID      int
	firstID         int
	contiguousBsize int64
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (d *blockEncoder) getRemoteBlockSize(id int) int64 {
	if d.remainder <= 0 {
		return 0
	}
	if id == d.lastBlockID {
		return min(d.lastBlockSize, d.remainder)
	}
	return min(d.blockSize, d.remainder)
}

func (d *blockEncoder) getLocalBlockSize(n int64) int64 {
	if d.remainder <= 0 {
		return 0
	}
	return min(n, d.remainder)
}

var errNoSpaceLeft = errors.New("blockEncoder: no space left")

// TODO: Occasionally tries to send 1-byte blobs.
func (d *blockEncoder) sendLocalBlock() error {
	if _, ok := d.prevID(); ok {
		err := d.flushReuseChunks()
		if err != nil {
			return err
		}
	}
	var hlen int64
	if hlen = d.getLocalBlockSize(d.r.HeadLen()); hlen <= 0 {
		return errNoSpaceLeft
	}
	err := d.enc.Encode(LocalBlockType)
	if err != nil {
		return err
	}
	err = d.enc.Encode(LocalBlock{
		Size: hlen,
		Off:  d.off,
	})
	if err != nil {
		return err
	}
	var b bytes.Buffer
	n, err := io.CopyN(d.enc, io.TeeReader(d.r.Head(), &b), hlen)
	log.Printf(
		"sendLocalBlock: size: %d, off: %d, data: %q",
		hlen, d.off, b.Bytes(),
	)
	d.off += n
	d.remainder -= n
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
	prevID, set := d.prevID()
	bsize := d.getRemoteBlockSize(id)
	if bsize <= 0 {
		if set {
			return d.flushReuseChunks()
		}
		return errNoSpaceLeft
	}
	d.r.Skip(int(bsize))
	d.remainder -= bsize
	log.Printf("remainder: %d bytes left", d.remainder)
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
	log.Printf(
		"flushReuseChunks: numChunks: %d, prevID: %d, d.firstID: %d, d.off: %d, remain: %d",
		numChunks, prevID, d.firstID, d.off, d.remainder,
	)
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
	blen := d.r.BufferedLen()
	if blen <= 0 {
		return nil
	}
	if blen = d.getLocalBlockSize(blen); blen <= 0 {
		return errNoSpaceLeft
	}
	err = d.enc.Encode(LocalBlockType)
	if err != nil {
		return err
	}
	err = d.enc.Encode(LocalBlock{
		Size: blen,
		Off:  d.off,
	})
	if err != nil {
		return err
	}
	var b bytes.Buffer
	n, err := io.CopyN(d.enc, io.TeeReader(d.r.Buffered(), &b), blen)
	log.Printf(
		"flush: localblock: size: %d, off: %d, sent: %d data: %q",
		blen, d.off, n, b.Bytes(),
	)
	d.off += n
	d.remainder -= n
	return err
}

type SrcFileLister struct {
	Root             string
	IncludeEmptyDirs bool
}

func (s *SrcFileLister) List() ([]SenderSrcFile, error) {
	var list []SenderSrcFile
	err := filepath.Walk(s.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("List: %w", err)
		}
		list, err = s.addSrcFile(list, path, info)
		return err
	})
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (s *SrcFileLister) AddSrcFile(list []SenderSrcFile, path string) ([]SenderSrcFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return s.addSrcFile(list, path, info)
}

func (s *SrcFileLister) addSrcFile(list []SenderSrcFile, path string, info os.FileInfo) ([]SenderSrcFile, error) {
	size := info.Size()
	if info.IsDir() {
		if s.IncludeEmptyDirs {
			if path == s.Root {
				return list, nil
			}
			if n := info.Name(); n == "." || n == ".." {
				return list, nil
			}
		} else {
			return list, nil
		}
		size = 0
	}
	rel, err := filepath.Rel(s.Root, path)
	if err != nil {
		return list, err
	}
	log.Printf("addSrcFile (%s): %d", info.Name(), size)
	list = append(list, SenderSrcFile{
		SrcFile: SrcFile{
			Path:  rel,
			Uid:   int(info.Sys().(*syscall.Stat_t).Uid),
			Gid:   int(info.Sys().(*syscall.Stat_t).Gid),
			Mode:  info.Mode(),
			Size:  size,
			Mtime: info.ModTime(),
		},
	})
	return list, nil
}

// Sender protocol is more or less as described below:
// - send the file list header (FileListHdr),
// - send as many source files submitted in the previous item,
// - and then read the same number of target files from the receiver side
// - remember, each target file contains (*DstFile).NumChunks() number of
//   blocks after it.
func SendSrcFileList(enc Encoder, list []SenderSrcFile, delete bool) error {
	hdr := FileListHdr{
		NumFiles:    len(list),
		Type:        SenderFileList,
		DeleteExtra: delete,
	}
	log.Printf("sent hdr: %#v", hdr)
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

func RecvDstFileList(dec Decoder, list []SenderSrcFile) (int, error) {
	var nrChanged int
	var hdr FileListHdr
	log.Printf("ptr 0")
	err := dec.Decode(&hdr)
	if err != nil {
		return 0, fmt.Errorf("failed to recv dst header: %w", err)
	}
	if hdr.Type != ReceiverFileList {
		return 0, fmt.Errorf("sender: invalid header type: %v", hdr.Type)
	}
	log.Printf("ptr 1")
	for i := 0; i < hdr.NumFiles; i++ {
		log.Printf("ptr 2")
		err := dec.Decode(&list[i].dst.DstFile)
		if err != nil {
			return nrChanged, fmt.Errorf("failed to recv dst list: %w", err)
		}
		// sanity check
		if id := list[i].dst.ID; id != i {
			return nrChanged, fmt.Errorf("dst file invalid ID got: %d, want: %d", id, i)
		}
		dst := &list[i].dst
		if dst.Type != DstFileIdentical {
			nrChanged++
		}
		dst.sums = make(map[uint32]SenderBlockSum)
		nrBlocks := dst.NumChunks()
		for j := 0; j < nrBlocks; j++ {
			var bs SenderBlockSum
			err := dec.Decode(&bs.BlockSum)
			if err != nil {
				return nrChanged, fmt.Errorf("recving block sum failed: %w", err)
			}
			bs.id = j
			if _, ok := dst.sums[bs.Rsum]; ok {
				// new := hex.EncodeToString(bs.Csum)
				// old := hex.EncodeToString(dst.sums[bs.Rsum].Csum)
				// return nrChanged, fmt.Errorf("duplicate block received: old: %q new: %q", old, new)
				continue
			}
			dst.sums[bs.Rsum] = bs
		}
	}
	return nrChanged, nil
}
