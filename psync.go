package main

import (
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/chmduquesne/rollinghash/adler32"
)

type MergeType byte

const (
	New MergeType = iota
	NotChanged
	Partial
)

type MergeHdr struct {
	ID  int
	Typ MergeType
}

type ChunkType byte

const (
	ReuseExisting ChunkType = iota
	Blob
)

type MergeReuse struct {
	ChunkID int
	Off     int64
}

type MergeBlob struct {
	Size, Off int64
}

func SendMergeInfo(b *BaseFile, enc Encoder) {
}

type SyncEnt struct {
	Path     string
	Uid, Gid int
	Mode     os.FileMode
	Size     int64
	Mtime    time.Time

	chunkSize int
}

type Encoder interface {
	Encode(e interface{}) error
}

type BaseFile struct {
	ID        int
	ChunkSize int
	Size      int64
}

func (b *BaseFile) NumChunks() int {
	return int((b.Size + (int64(b.ChunkSize) - 1)) / int64(b.ChunkSize))
}

type Chunk struct {
	Rsum uint32
	Sum  []byte
}

func chunkFile(path string, enc Encoder, blockSize int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sum := md5.New()
	rol := adler32.New()
	w := io.MultiWriter(sum, rol)
	for err == nil {
		var n int64
		if n, err = io.CopyN(w, f, int64(blockSize)); err != nil {
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

func SendBaseFiles(root string, chunkSize int, list []SyncEnt, enc Encoder) error {
	for i, v := range list {
		path := filepath.Join(root, v.Path)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if err := enc.Encode(BaseFile{ID: i}); err != nil {
					return err
				}
				continue
			}
			return nil
		}
		if info.ModTime() == v.Mtime && info.Size() == v.Size {
			continue
		}
		if err := enc.Encode(BaseFile{
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

func GenSyncList(root string) ([]SyncEnt, error) {
	var list []SyncEnt
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		list = append(list, SyncEnt{
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
