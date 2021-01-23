package main

import (
	"io/ioutil"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// A fileStat is the implementation of FileInfo returned by Stat and Lstat.
// extracted from package os
type fileStat struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	sys     syscall.Stat_t
}

func (fs *fileStat) Size() int64        { return fs.size }
func (fs *fileStat) Mode() os.FileMode  { return fs.mode }
func (fs *fileStat) ModTime() time.Time { return fs.modTime }
func (fs *fileStat) Sys() interface{}   { return &fs.sys }
func (fs *fileStat) Name() string       { return fs.name }
func (fs *fileStat) IsDir() bool        { return fs.Mode().IsDir() }

func TestSendDstFileList(t *testing.T) {
	in := []ReceiverSrcFile{
		{
			SrcFile: SrcFile{
				Path:  "path/to/foo.txt",
				Size:  0,
				Mtime: time.Time{},
			},
			dstFileSize: 0,
			chunkSize:   0,
		},
		{
			SrcFile: SrcFile{
				Path:  "path/to/newfile.txt",
				Size:  0,
				Mtime: time.Time{},
			},
			dstFileSize: 0,
			chunkSize:   0,
		},
		{
			SrcFile: SrcFile{
				Path:  "path/to/similar_file",
				Size:  int64(len(orig)),
				Mtime: time.Now(),
			},
			dstFileSize: 0,
			chunkSize:   0,
		},
	}
	stats := map[string]struct {
		fs  fileStat
		err error
	}{
		"rootdir/path/to/foo.txt": {
			fileStat{
				size:    0,
				modTime: time.Time{},
			},
			nil,
		},
		"rootdir/path/to/similar_file": {
			fileStat{
				size:    int64(len(orig)),
				modTime: time.Time{},
			},
			nil,
		},
	}
	osStat = func(name string) (os.FileInfo, error) {
		if stent, ok := stats[name]; ok {
			return &stent.fs, stent.err
		}
		return nil, os.ErrNotExist
	}
	defer func() { osStat = os.Stat }()

	sendChunks = func(path string, enc Encoder, blockSize int) error {
		r := ioutil.NopCloser(strings.NewReader(orig))
		return doChunkFile(r, enc, blockSize)
	}
	defer func() { sendChunks = chunkFile }()

	var enc mergeDscEnc
	err := sendDstFileList("rootdir", 8, in, &enc)
	if err != nil {
		t.Fatal(err)
	}
	diff := cmp.Diff("", enc)
	// t.Errorf("%#v", enc)
	t.Errorf("%s", diff)
	// receiver_test.go|97|
	// main.mergeDscEnc{(*main.FileListHdr)(0xc000018470),
	// main.DstFile{ID:0, ChunkSize:0, Size:0, Type:1}, main.DstFile{ID:1,
	// ChunkSize:0, Size:0, Type:2}, main.DstFile{ID:2, ChunkSize:8,
	// Size:57, Type:0}, main.BlockSum{Rsum:0x71c019d, Csum:[]uint8{0x2e,
	// 0x9e, 0xc3, 0x17, 0xe1, 0x97, 0x81, 0x93, 0x58, 0xfb, 0xc4, 0x3a,
	// 0xfc, 0xa7, 0xd8, 0x37}}, main.BlockSum{Rsum:0xa3a0291,
	// Csum:[]uint8{0x9, 0x71, 0xea, 0x36, 0x56, 0xf, 0x19, 0xd, 0x33,
	// 0x25, 0x7a, 0x37, 0x22, 0xf2, 0xb0, 0x8c}},
	// main.BlockSum{Rsum:0xc1402ea, Csum:[]uint8{0x6f, 0x1a, 0xdb, 0xa1,
	// 0xb0, 0x7b, 0x80, 0x42, 0xab, 0x76, 0x14, 0x4a, 0x2b, 0xc9, 0x8f,
	// 0x86}}, main.BlockSum{Rsum:0xfb00385, Csum:[]uint8{0xa7, 0x9, 0x0,
	// 0x0, 0x6e, 0x6c, 0x6e, 0x51, 0xd, 0x50, 0x18, 0x65, 0xa9, 0xf6,
	// 0x5e, 0xfd}}, main.BlockSum{Rsum:0xfc20328, Csum:[]uint8{0xaa, 0x7e,
	// 0x6f, 0x7a, 0xf8, 0xd9, 0xf4, 0xce, 0x4b, 0xbe, 0x37, 0xc9, 0x96,
	// 0x45, 0x6, 0x8a}}, main.BlockSum{Rsum:0xd790309, Csum:[]uint8{0x7f,
	// 0x75, 0x67, 0x2f, 0xf, 0x60, 0x12, 0x5b, 0x9d, 0x78, 0xfc
}
