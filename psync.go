package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/chmduquesne/rollinghash/adler32"
)

type SyncEnt struct {
	Path     string
	Uid, Gid int
	Mode     os.FileMode
	Size     int64
	Mtime    time.Time
}

type BaseFile struct {
	ID        int
	ChunkSize int
	Chunks    []Chunk
}

type Chunk struct {
	Rsum uint32
	Sum  []byte
}

func GenBaseFiles(root string, list []SyncEnt) error {
	for _, v := range list {
		path := filepath.Join(root, v.Path)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.ModTime() == v.Mtime && info.Size() == v.Size {
			continue
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
