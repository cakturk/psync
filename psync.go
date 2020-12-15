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
}

type MergeType byte

const (
	New MergeType = iota
	NotChanged
	Partial
)

type MergeDesc struct {
	ID  int
	Typ MergeType
}

type ChunkType byte

const (
	ReuseExisting ChunkType = iota
	Blob
)

type MergeReuse struct {
	ChunkID  int
	NrChunks int
	Off      int64
}

type MergeBlob struct {
	Size, Off int64
}

type ChunkWithID struct {
	ID int // Chunk ID (index of chunk)
	Chunk
}

type Sender struct {
	r        io.ReadWriter
	enc      Encoder
	root     string
	srcFiles []SrcFile
}

var errShortRead = errors.New("unexpected EOF")

func sendMergeDescs(r io.ReadSeeker, e *SrcFile, enc Encoder) error {
	var rr Ring
	b := bufio.NewReader(r)
	mh := md5.New()
	rh := adler32.New()
	tr := io.TeeReader(b, &rr)
	// write initial window
	if n, err := io.CopyN(rh, tr, int64(e.base.ChunkSize)); err != nil {
		if err != io.EOF {
			return err
		}
		if n == 0 {
			return errShortRead
		}
	}
	for {
		c, err := b.ReadByte()
		if err != nil {
			log.Printf("sendDirections: c: %d, %v", c, err)
			if err == io.EOF {
				break
			}
			return err
		}
		rh.Roll(c)
		rr.WriteByte(c)
		ch, ok := e.base.chunks[rh.Sum32()]
		// enc.Encode(rh.Sum32())
		if !ok {
			continue
		}
		// Check for false positive adler32 matches
		mh.Reset()
		io.CopyN(mh, &rr, int64(e.base.ChunkSize))
		if !bytes.Equal(mh.Sum(nil), ch.Sum) {
			continue
		}
		_, err = r.Seek(int64(e.base.ChunkSize), io.SeekCurrent)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Sender) sendDirections(e *SrcFile) error {
	if e.Size == 0 {
		return nil
	}
	f, err := os.Open(filepath.Join(s.root, e.Path))
	if err != nil {
		return err
	}
	defer f.Close()
	return sendMergeDescs(f, e, s.enc)
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

type DstFile struct {
	ID        int
	ChunkSize int // 0 means this is a new file
	Size      int64

	chunks map[uint32]ChunkWithID // used by sender
}

func (b *DstFile) NumChunks() int {
	return int((b.Size + (int64(b.ChunkSize) - 1)) / int64(b.ChunkSize))
}

type Chunk struct {
	Rsum uint32
	Sum  []byte
}

func (c *Chunk) String() string {
	// panic("noooo")
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
