package psync

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSendSrcFileList(t *testing.T) {
	in := []SenderSrcFile{
		{
			SrcFile: SrcFile{
				Path: "path/to/file1.bin",
				Uid:  1000,
				Gid:  1003,
				Mode: 0644,
				Size: 233348971,
			},
		},
		{
			SrcFile: SrcFile{
				Path: "path/to/subdir/uboot.dtb",
				Uid:  500,
				Gid:  500,
				Mode: 0600,
				Size: 4329918,
			},
		},
	}
	var enc mergeDscEnc
	err := SendSrcFileList(&enc, in, false)
	if err != nil {
		t.Fatal(err)
	}
	want := mergeDscEnc{
		&FileListHdr{
			NumFiles: len(in),
			Type:     SenderFileList,
		},
	}
	for i := range in {
		want = append(want, &in[i].SrcFile)
	}
	diff := cmp.Diff(want, enc)
	if diff != "" {
		t.Errorf("sendSrcFileList(...) mismatch (-want +got):\n%s", diff)
	}
}

func TestRecvDstFileList(t *testing.T) {
	digest := func(s string) []byte { return digest(t, s) }
	const nrFiles = 5
	hdr := &FileListHdr{
		NumFiles: nrFiles,
		Type:     ReceiverFileList,
	}
	in := []interface{}{
		hdr,
		&DstFile{
			ID:        0,
			ChunkSize: 8,
			Size:      int64(len(orig)),
		},
		BlockSum{Rsum: 0x071c019d, Csum: digest("2e9ec317e197819358fbc43afca7d837")},
		BlockSum{Rsum: 0x0a3a0291, Csum: digest("0971ea36560f190d33257a3722f2b08c")},
		BlockSum{Rsum: 0x0c1402ea, Csum: digest("6f1adba1b07b8042ab76144a2bc98f86")},
		BlockSum{Rsum: 0x0fb00385, Csum: digest("a70900006e6c6e510d501865a9f65efd")},
		BlockSum{Rsum: 0x0fc20328, Csum: digest("aa7e6f7af8d9f4ce4bbe37c99645068a")},
		BlockSum{Rsum: 0x0d790309, Csum: digest("7f75672f0f60125b9d78fc51fd5c3614")},
		BlockSum{Rsum: 0x0d090302, Csum: digest("008f7a640603fa380ae5fa52eddb1f9f")},
		BlockSum{Rsum: 0x000b000b, Csum: digest("68b329da9893e34099c7d8ad5cb9c940")},
		// New file
		&DstFile{
			ID:        1,
			ChunkSize: 8,
			Type:      DstFileNotExist,
		},
		&DstFile{
			ID:        2,
			ChunkSize: 8,
			Size:      20,
		},
		BlockSum{Rsum: 0x071c019d, Csum: digest("2e9ec317e197819358fbc43afca7d837")},
		BlockSum{Rsum: 0x0c1402ea, Csum: digest("6f1adba1b07b8042ab76144a2bc98f86")},
		BlockSum{Rsum: 0x0d790309, Csum: digest("7f75672f0f60125b9d78fc51fd5c3614")},
		&DstFile{
			ID:        3,
			ChunkSize: 4,
			Size:      12,
		},
		BlockSum{Rsum: 0x071c019d, Csum: digest("2e9ec317e197819358fbc43afca7d837")},
		BlockSum{Rsum: 0x0c1402ea, Csum: digest("6f1adba1b07b8042ab76144a2bc98f86")},
		BlockSum{Rsum: 0x0d790309, Csum: digest("7f75672f0f60125b9d78fc51fd5c3614")},
		&DstFile{
			ID:   4,
			Type: DstFileIdentical,
		},
	}
	dec := createFakeDecoder(in...)
	list := make([]SenderSrcFile, nrFiles)
	_, err := RecvDstFileList(dec, list)
	if err != nil {
		t.Fatal(err)
	}
	var got []interface{}
	got = append(got, hdr)
	for i := range list {
		s := list[i]
		got = append(got, &s.dst.DstFile)
		numBlocks := s.dst.NumChunks()
		if numBlocks <= 0 {
			continue
		}
		sums := make([]interface{}, s.dst.NumChunks())
		for _, bs := range s.dst.sums {
			sums[bs.id] = bs.BlockSum
		}
		got = append(got, sums...)
	}
	if diff := cmp.Diff(in, got); diff != "" {
		t.Errorf("recvDstFileList(...) mismatch (-want +got):\n%s", diff)
	}
}
