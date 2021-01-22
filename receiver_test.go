package main

import (
	"os"
	"syscall"
	"testing"
	"time"
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
				Uid:   0,
				Gid:   0,
				Mode:  0,
				Size:  0,
				Mtime: time.Time{},
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
	}
	osStat = func(name string) (os.FileInfo, error) {
		if stent, ok := stats[name]; ok {
			return &stent.fs, stent.err
		}
		return nil, os.ErrNotExist
	}
	defer func() { osStat = os.Stat }()
	var enc mergeDscEnc
	err := sendDstFileList("rootdir", 8, in, &enc)
	if err != nil {
		t.Fatal(err)
	}
	t.Errorf("%+v", enc)
}
