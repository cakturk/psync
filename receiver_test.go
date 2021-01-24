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
	digest := func(s string) []byte { return digest(t, s) }
	in := []ReceiverSrcFile{
		{
			SrcFile: SrcFile{
				Path:  "path/to/identical.txt",
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
		"rootdir/path/to/identical.txt": {
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
	want := mergeDscEnc{
		&FileListHdr{NumFiles: 3, Type: ReceiverFileList},
		DstFile{Type: DstFileIdentical},
		DstFile{ID: 1, Type: DstFileNotExist},
		DstFile{ID: 2, ChunkSize: 8, Size: 57},
		BlockSum{Rsum: 0x071c019d, Csum: digest("2e9ec317e197819358fbc43afca7d837")},
		BlockSum{Rsum: 0x0a3a0291, Csum: digest("0971ea36560f190d33257a3722f2b08c")},
		BlockSum{Rsum: 0x0c1402ea, Csum: digest("6f1adba1b07b8042ab76144a2bc98f86")},
		BlockSum{Rsum: 0x0fb00385, Csum: digest("a70900006e6c6e510d501865a9f65efd")},
		BlockSum{Rsum: 0x0fc20328, Csum: digest("aa7e6f7af8d9f4ce4bbe37c99645068a")},
		BlockSum{Rsum: 0x0d790309, Csum: digest("7f75672f0f60125b9d78fc51fd5c3614")},
		BlockSum{Rsum: 0x0d090302, Csum: digest("008f7a640603fa380ae5fa52eddb1f9f")},
		BlockSum{Rsum: 0x000b000b, Csum: digest("68b329da9893e34099c7d8ad5cb9c940")},
	}
	var enc mergeDscEnc
	err := sendDstFileList("rootdir", 8, in, &enc)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, enc); diff != "" {
		t.Errorf("sendDstFileList(...) mismatch (-want +got):\n%s", diff)
	}
}

func TestRecvSrcFileList(t *testing.T) {
	const nrFiles = 2
	hdr := &FileListHdr{
		NumFiles: nrFiles,
		Type:     SenderFileList,
	}
	in := []interface{}{
		hdr,
		&SrcFile{
			Path: "path/to/file1.bin",
			Uid:  1000,
			Gid:  1003,
			Mode: 0644,
			Size: 233348971,
		},
		&SrcFile{
			Path: "path/to/subdir/uboot.dtb",
			Uid:  500,
			Gid:  500,
			Mode: 0600,
			Size: 4329918,
		},
	}
	dec := createFakeDecoder(in...)
	list, err := recvSrcFileList(dec)
	if err != nil {
		t.Fatal(err)
	}
	var got []interface{}
	got = append(got, hdr)
	for i := range list {
		s := list[i]
		got = append(got, &s.SrcFile)
	}
	if diff := cmp.Diff(in, got); diff != "" {
		t.Errorf("recvSrcFileList(...) mismatch (-want +got):\n%s", diff)
	}
}
