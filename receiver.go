package psync

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	stdadler32 "hash/adler32"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
)

type ReceiverSrcFile struct {
	SrcFile

	// following fields are not serialized
	dstFileSize int64
	chunkSize   int // used by receiver only
}

type Receiver struct {
	Root string
	Dec  DecodeReader
}

func (r *Receiver) BuildFiles(nrChangedFiles int, srcFiles []ReceiverSrcFile) error {
	for i := 0; i < nrChangedFiles; i++ {
		err := r.buildFile(srcFiles)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Receiver) buildFile(srcFiles []ReceiverSrcFile) error {
	var fd FileDesc
	if err := r.Dec.Decode(&fd); err != nil {
		return fmt.Errorf("buildfile: %w", err)
	}
	if fd.ID < 0 && fd.ID > len(srcFiles) {
		return fmt.Errorf("there is no such file with id: %d", fd.ID)
	}
	// handle new file scenario do io.Copy or something like that
	if fd.Typ == NewFile {
		return r.create(&srcFiles[fd.ID])
	}
	if fd.Typ != PartialFile {
		return fmt.Errorf("unrecognized file descriptor type: %v", fd.Typ)
	}
	// TODO: if we send file descriptors and create files at the same
	// time, this temporary file may end up in the receiver file list,
	// which is not we want.
	tmp, err := ioutil.TempFile(r.Root, "psync*.tmp")
	// tmp, err := ioutil.TempFile("/tmp/cache", "psync*.tmp")
	if err != nil {
		return err
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	s := &srcFiles[fd.ID]
	f, err := os.Open(filepath.Join(r.Root, s.Path))
	if err != nil {
		return err
	}
	defer f.Close()
	if err = r.merge(s, f, tmp); err != nil {
		return err
	}
	if err := tmp.Chmod(s.Mode); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), f.Name()); err != nil {
		return err
	}
	return os.Chtimes(f.Name(), s.Mtime, s.Mtime)
}

func (r *Receiver) merge(s *ReceiverSrcFile, rd io.ReaderAt, tmp io.Writer) error {
	fb, err := os.Create("/tmp/pp.log")
	if err != nil {
		panic(err)
	}
	defer fb.Close()
	sum := md5.New()
	tmp = io.MultiWriter(tmp, sum, fb)
	var off int64
	for off < s.Size {
		var typ BlockType
		if err := r.Dec.Decode(&typ); err != nil {
			if err == io.EOF {
				break
			}
			// runtime.Breakpoint()
			return fmt.Errorf("failed to decode BlockType (%d/%d): %w", off, s.Size, err)
		}
		switch typ {
		case LocalBlockType:
			var lb LocalBlock
			var b bytes.Buffer
			if err := r.Dec.Decode(&lb); err != nil {
				// runtime.Breakpoint()
				return err
			}
			if off != lb.Off {
				return fmt.Errorf("local bad file offset: want %d, got: %d", lb.Off, off)
			}
			n, err := io.CopyN(io.MultiWriter(tmp, &b), r.Dec, lb.Size)
			off += n
			if err != nil {
				return err
			}
		case RemoteBlockType:
			var rb RemoteBlock
			var b bytes.Buffer
			if err := r.Dec.Decode(&rb); err != nil {
				// runtime.Breakpoint()
				return err
			}
			// XXX: rb.Off is not a remote file offset. Instead it
			// is the local, newly created file's write offset. The
			// read offset should be something like rb.ChunkID *
			// chunkSize.
			// XXX: Currently, we are not using the write offset
			// assuming all the block descriptors (RemoteBlock,
			// LocalBlock) are received sequentially, thus we assume
			// that the file's current write offset is the valid
			// file offset. However, this assumption could lead to
			// subtle errors if we send descriptors out of order.
			if off != rb.Off {
				return fmt.Errorf("remote bad file offset: want %d, got: %d", rb.Off, off)
			}
			n, err := io.Copy(
				io.MultiWriter(tmp, &b),
				io.NewSectionReader(
					rd,
					int64(rb.ChunkID*s.chunkSize),
					int64(rb.NrChunks*s.chunkSize),
				),
			)
			off += n
			if err != nil {
				// last block may be smaller than the others. So check
				// the file size first to see if this is an error we can
				// perfectly ignore.
				if err == io.EOF && off == s.Size {
					break
				}
				return err
			}
		default:
			panic("should not happen")
		}
	}
	// TODO: check exact file size before returning?
	if off != s.Size {
		return fmt.Errorf("unexpected EOF: off: %d, size: %d", off, s.Size)
	}
	var (
		typ     BlockType
		fileSum []byte
	)
	if err := r.Dec.Decode(&typ); err != nil {
		return fmt.Errorf("failed to decode FileSum: %w", err)
	}
	if typ != FileSum {
		return fmt.Errorf("unexpected block type: %v", typ)
	}
	if err := r.Dec.Decode(&fileSum); err != nil {
		return err
	}
	if csum := sum.Sum(nil); !bytes.Equal(csum, fileSum) {
		got := hex.EncodeToString(csum)
		want := hex.EncodeToString(fileSum)
		return fmt.Errorf(
			"checksum of file does not match the original: got: %q want %q",
			got, want,
		)
	}
	return nil
}

func (r *Receiver) create(s *ReceiverSrcFile) error {
	name := filepath.Join(r.Root, s.Path)
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, s.Mode)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.CopyN(f, r.Dec, s.Size)
	if err != nil {
		return err
	}
	if n != s.Size {
		return fmt.Errorf(
			"new file size mismatch (%s): got %d, want %d",
			name, n, s.Size,
		)
	}
	return os.Chtimes(name, s.Mtime, s.Mtime)
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
		if err := enc.Encode(BlockSum{
			Rsum: rol.Sum32(),
			Csum: sum.Sum(nil),
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
	}
	defer f.Close()
	return doChunkFile(f, enc, blockSize)
}

// modified during testing
var (
	osStat     = os.Stat
	sendChunks = chunkFile
)

// TODO: Can we improve this function so that we don't need to send anything
// back to the sender when there is no change in the directory tree?
func SendDstFileList(root string, chunkSize int, list []ReceiverSrcFile, enc Encoder) (int, error) {
	var nrChanged int
	hdr := FileListHdr{
		NumFiles: len(list),
		Type:     ReceiverFileList,
	}
	err := enc.Encode(&hdr)
	if err != nil {
		return 0, fmt.Errorf("sending dst list header failed: %w", err)
	}
	for i, v := range list {
		path := filepath.Join(root, v.Path)
		info, err := osStat(path)
		if err != nil {
			if os.IsNotExist(err) {
				nrChanged++
				if err := enc.Encode(DstFile{
					ID:   i,
					Type: DstFileNotExist,
				}); err != nil {
					return nrChanged, err
				}
				continue
			}
			return nrChanged, err
		}
		if v.Mode.IsDir() && !info.IsDir() {
			return 0, errors.New("file type mismatch")
		}
		if info.IsDir() || info.ModTime() == v.Mtime && info.Size() == v.Size {
			if err := enc.Encode(DstFile{
				ID:   i,
				Type: DstFileIdentical,
			}); err != nil {
				return nrChanged, err
			}
			continue
		}
		nrChanged++
		if err := enc.Encode(DstFile{
			ID:        i,
			ChunkSize: chunkSize,
			Size:      info.Size(),
			Type:      DstFileSimilar,
		}); err != nil {
			return nrChanged, err
		}
		list[i].chunkSize = chunkSize
		list[i].dstFileSize = info.Size()
		if err := sendChunks(path, enc, chunkSize); err != nil {
			return nrChanged, err
		}
	}
	return nrChanged, nil
}

// MkDirs create all the empty directories in the src file list
func MkDirs(list []ReceiverSrcFile, root string) error {
	for _, v := range list {
		if v.Mode.IsDir() {
			if err := os.MkdirAll(filepath.Join(root, v.Path), 0755); err != nil {
				return err
			}
		}
	}
	return nil
}

func DeleteExtra(list []ReceiverSrcFile, root string) error {
	files := make(map[string]bool)
	for _, m := range list {
		files[filepath.Join(root, m.Path)] = true
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path == root {
			return nil
		}
		if n := info.Name(); n == "." || n == ".." {
			return nil
		}
		if exist := files[path]; !exist {
			err := os.RemoveAll(path)
			if err != nil {
				log.Printf("RemoveAll: %v", err)
			}
		}
		return nil
	})
	return err
}

func RecvSrcFileList(dec Decoder) ([]ReceiverSrcFile, bool, error) {
	var hdr FileListHdr
	err := dec.Decode(&hdr)
	if err != nil {
		return nil, false, fmt.Errorf("failed to recv src file list header: %w", err)
	}
	if hdr.Type != SenderFileList {
		return nil, false, fmt.Errorf("receiver: invalid header type: %v", hdr.Type)
	}
	list := make([]ReceiverSrcFile, hdr.NumFiles)
	for i := range list {
		err := dec.Decode(&list[i].SrcFile)
		if err != nil {
			return nil, false, fmt.Errorf("recving src list failed: %w", err)
		}
	}
	return list, hdr.DeleteExtra, nil
}
