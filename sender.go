package psync

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
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
	for i := range files {
		sf := &files[i]
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
Outer:
	for {
		var n int64
		// fill in the buffer
		rh.Reset()
		n, err = io.CopyN(rh, &cr, chunkSize)
		if err != nil {
			if err != io.EOF {
				return err
			}
			if n == 0 {
				break
			}
		}
		ch, ok := e.dst.sums[rh.Sum32()]
		if ok {
			mh.Reset()
			io.CopyN(mh, cr.Tail(), chunkSize)
			if bytes.Equal(mh.Sum(nil), ch.Csum) {
				if cr.HeadLen() > 0 {
					err = ben.sendLocalBlock()
					if err != nil {
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
				if cr.HeadLen() > 0 {
					err = ben.sendLocalBlock()
					if err != nil {
						return err
					}
				}
				ben.sendRemoteBlock(ch.id)
				continue Outer
			}
		}
		err = ben.sendLocalBlock()
		if err != nil {
			return err
		}
	}
	err = ben.flush()
	if err != nil {
		return err
	}
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
	enc        EncodeWriter
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
	hlen := d.r.HeadLen()
	err = d.enc.Encode(LocalBlock{
		Size: hlen,
		Off:  d.off,
	})
	if err != nil {
		return err
	}
	var b bytes.Buffer
	n, err := io.Copy(d.enc, io.TeeReader(d.r.Head(), &b))
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
	blen := d.r.BufferedLen()
	err = d.enc.Encode(LocalBlock{
		Size: blen,
		Off:  d.off,
	})
	if err != nil {
		return err
	}
	var b bytes.Buffer
	n, err := io.Copy(d.enc, io.TeeReader(d.r.Buffered(), &b))
	d.off += n
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
	err := dec.Decode(&hdr)
	if err != nil {
		return 0, fmt.Errorf("failed to recv dst header: %w", err)
	}
	if hdr.Type != ReceiverFileList {
		return 0, fmt.Errorf("sender: invalid header type: %v", hdr.Type)
	}
	for i := 0; i < hdr.NumFiles; i++ {
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
