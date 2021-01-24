// Code generated by "stringer -type=FileType,FileListType,DstFileType,BlockType -output types_string.go"; DO NOT EDIT.

package main

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[NewFile-0]
	_ = x[PartialFile-1]
}

const _FileType_name = "NewFilePartialFile"

var _FileType_index = [...]uint8{0, 7, 18}

func (i FileType) String() string {
	if i >= FileType(len(_FileType_index)-1) {
		return "FileType(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _FileType_name[_FileType_index[i]:_FileType_index[i+1]]
}
func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[InvalidFileListType-0]
	_ = x[SenderFileList-1]
	_ = x[ReceiverFileList-2]
}

const _FileListType_name = "InvalidFileListTypeSenderFileListReceiverFileList"

var _FileListType_index = [...]uint8{0, 19, 33, 49}

func (i FileListType) String() string {
	if i >= FileListType(len(_FileListType_index)-1) {
		return "FileListType(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _FileListType_name[_FileListType_index[i]:_FileListType_index[i+1]]
}
func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[DstFileSimilar-0]
	_ = x[DstFileIdentical-1]
	_ = x[DstFileNotExist-2]
}

const _DstFileType_name = "DstFileSimilarDstFileIdenticalDstFileNotExist"

var _DstFileType_index = [...]uint8{0, 14, 30, 45}

func (i DstFileType) String() string {
	if i < 0 || i >= DstFileType(len(_DstFileType_index)-1) {
		return "DstFileType(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _DstFileType_name[_DstFileType_index[i]:_DstFileType_index[i+1]]
}
func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[RemoteBlockType-0]
	_ = x[LocalBlockType-1]
	_ = x[FileSum-2]
}

const _BlockType_name = "RemoteBlockTypeLocalBlockTypeFileSum"

var _BlockType_index = [...]uint8{0, 15, 29, 36}

func (i BlockType) String() string {
	if i >= BlockType(len(_BlockType_index)-1) {
		return "BlockType(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _BlockType_name[_BlockType_index[i]:_BlockType_index[i+1]]
}
