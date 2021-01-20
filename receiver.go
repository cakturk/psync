package main

import (
	"bytes"
	"crypto/md5"
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
	root     string
	srcFiles []ReceiverSrcFile
	dec      Decoder
}

func (r *Receiver) buildFile() error {
	var fd FileDesc
	if err := r.dec.Decode(&fd); err != nil {
		return err
	}
	if fd.ID < 0 && fd.ID > len(r.srcFiles) {
		return fmt.Errorf("there is no such file with id: %d", fd.ID)
	}
	// handle new file scenario do io.Copy or something like that
	if fd.Typ == NewFile {
		return r.create(&r.srcFiles[fd.ID])
	}
	if fd.Typ != PartialFile {
		return fmt.Errorf("unrecognized file descriptor type: %v", fd.Typ)
	}
	tmp, err := ioutil.TempFile("", "psync*.tmp")
	if err != nil {
		return err
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	s := &r.srcFiles[fd.ID]
	f, err := os.Open(filepath.Join(r.root, s.Path))
	if err != nil {
		return err
	}
	defer f.Close()
	if err = r.merge(s, f, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), f.Name()); err != nil {
		return err
	}
	return os.Chtimes(f.Name(), s.Mtime, s.Mtime)
}

func (r *Receiver) merge(s *ReceiverSrcFile, rd io.ReaderAt, tmp io.Writer) error {
	sum := md5.New()
	tmp = io.MultiWriter(tmp, sum)
	var off int64
	for off < s.Size {
		var typ BlockType
		if err := r.dec.Decode(&typ); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		switch typ {
		case LocalBlockType:
			var lb LocalBlock
			if err := r.dec.Decode(&lb); err != nil {
				return err
			}
			if off != lb.Off {
				return fmt.Errorf("local bad file offset: want %d, got: %d", lb.Off, off)
			}
			n, err := io.CopyN(tmp, r.dec, lb.Size)
			off += n
			if err != nil {
				return err
			}
		case RemoteBlockType:
			var rb RemoteBlock
			if err := r.dec.Decode(&rb); err != nil {
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
				tmp,
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
	if off < s.Size {
		return fmt.Errorf("unexpected EOF: off: %d, size: %d", off, s.Size)
	}
	var (
		typ     BlockType
		fileSum []byte
	)
	if err := r.dec.Decode(&typ); err != nil {
		return err
	}
	if typ != FileSum {
		return fmt.Errorf("unexpected block type: %v", typ)
	}
	if err := r.dec.Decode(&fileSum); err != nil {
		log.Printf("sum: %v", fileSum)
		return err
	}
	if csum := sum.Sum(nil); !bytes.Equal(csum, fileSum) {
		return errors.New("checksum of file does not match the original")
	}
	return nil
}

func (r *Receiver) create(s *ReceiverSrcFile) error {
	name := filepath.Join(r.root, s.Path)
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, s.Mode)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, r.dec)
	if err != nil {
		return err
	}
	if n != s.Size {
		return fmt.Errorf(
			"new file size mismatch: got %d, want %d",
			n, s.Size,
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
		return err
	}
	defer f.Close()
	return doChunkFile(f, enc, blockSize)
}

func sendDstFileList(root string, chunkSize int, list []ReceiverSrcFile, enc Encoder) error {
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
		list[i].dstFileSize = info.Size()
		if err := chunkFile(path, enc, chunkSize); err != nil {
			return err
		}
	}
	return nil
}

func recvSrcFileList(dec Decoder) ([]ReceiverSrcFile, error) {
	var nrBlocks int
	err := dec.Decode(&nrBlocks)
	if err != nil {
		return nil, fmt.Errorf("failed to recv src file list header: %w", err)
	}
	list := make([]ReceiverSrcFile, nrBlocks)
	for i := range list {
		err := dec.Decode(&list[i].SrcFile)
		if err != nil {
			return nil, fmt.Errorf("recving src list failed: %w", err)
		}
	}
	return list, nil
}
