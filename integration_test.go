package psync_test

import (
	"bufio"
	"encoding/gob"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/cakturk/psync"
	"github.com/google/go-cmp/cmp"
)

// decReader combines an io.Reader with a Decoder for the Receiver
type decReader struct {
	io.Reader
	psync.Decoder
}

// encWriter combines an io.Writer with an Encoder for the Sender
type encWriter struct {
	io.Writer
	psync.Encoder
}

// Helper to run server side of protocol
func runServerSide(t *testing.T, conn net.Conn, targetDir string, blockSize int) error {
	defer conn.Close()

	// Read handshake
	h, err := psync.ReadHandshake(conn)
	if err != nil {
		return err
	}
	if !h.Valid() {
		t.Error("invalid handshake")
		return nil
	}

	// Setup encoder/decoder with buffering
	br := bufio.NewReader(conn)
	dec := gob.NewDecoder(br)
	enc := gob.NewEncoder(conn)

	// Receive source file list
	srcFiles, deleteExtra, err := psync.RecvSrcFileList(dec)
	if err != nil {
		return err
	}

	// Handle deletions if requested
	if deleteExtra {
		if err := psync.DeleteExtra(srcFiles, targetDir); err != nil {
			return err
		}
	}

	// Create empty directories
	if err := psync.MkDirs(srcFiles, targetDir); err != nil {
		return err
	}

	// Send destination file list
	n, err := psync.SendDstFileList(targetDir, blockSize, srcFiles, enc)
	if err != nil {
		return err
	}

	if n == 0 {
		return nil
	}

	// Receive and build files
	rcv := psync.Receiver{
		Root: targetDir,
		Dec: decReader{
			Reader:  br,
			Decoder: dec,
		},
	}
	if err := rcv.BuildFiles(n, srcFiles); err != nil {
		return err
	}

	// Send acknowledgment
	return enc.Encode(uint32(0x1a2b))
}

// Helper to run client side of protocol
func runClientSide(t *testing.T, conn net.Conn, sourceDir string, allowEmptyDirs bool) error {
	defer conn.Close()

	// Send handshake
	hs := psync.NewHandshake(1, psync.WireFormatGob, 0)
	if _, err := hs.WriteTo(conn); err != nil {
		return err
	}

	// Generate file list
	lister := psync.SrcFileLister{
		Root:             sourceDir,
		IncludeEmptyDirs: allowEmptyDirs,
	}
	fileList, err := lister.List()
	if err != nil {
		return err
	}

	// Setup encoder/decoder
	enc := gob.NewEncoder(conn)
	dec := gob.NewDecoder(conn)

	// Send source file list
	if err := psync.SendSrcFileList(enc, fileList, true); err != nil {
		return err
	}

	// Receive destination file list
	n, err := psync.RecvDstFileList(dec, fileList)
	if err != nil {
		return err
	}

	if n == 0 {
		return nil
	}

	// Send block descriptors
	sender := psync.Sender{
		Enc: encWriter{
			Writer:  conn,
			Encoder: enc,
		},
		Root: sourceDir,
	}
	if err := sender.SendBlockDescList(fileList); err != nil {
		return err
	}

	// Receive acknowledgment
	var ack uint32
	return dec.Decode(&ack)
}

// TestBasicSync tests a single file synchronization using net.Pipe
func TestBasicSync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(sourceDir, "test.txt")
	testContent := []byte("hello world")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create in-memory connection
	clientConn, serverConn := net.Pipe()

	// Run server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runServerSide(t, serverConn, targetDir, 512)
	}()

	// Run client
	clientErr := runClientSide(t, clientConn, sourceDir, true)
	if clientErr != nil {
		t.Fatalf("client failed: %v", clientErr)
	}

	// Wait for server
	if err := <-serverErr; err != nil {
		t.Fatalf("server failed: %v", err)
	}

	// Verify file was synced
	targetFile := filepath.Join(targetDir, "test.txt")
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read synced file: %v", err)
	}

	if diff := cmp.Diff(testContent, content); diff != "" {
		t.Errorf("content mismatch (-want +got):\n%s", diff)
	}
}

// TestMultipleFilesSync tests syncing multiple files and directories
func TestMultipleFilesSync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create multiple files
	files := map[string]string{
		"file1.txt":              "content 1",
		"file2.txt":              "content 2",
		"subdir/file3.txt":       "content 3",
		"subdir/sub2/file4.txt":  "content 4",
	}

	for path, content := range files {
		fullPath := filepath.Join(sourceDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", path, err)
		}
	}

	// Sync
	clientConn, serverConn := net.Pipe()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runServerSide(t, serverConn, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
		t.Fatalf("client failed: %v", err)
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("server failed: %v", err)
	}

	// Verify all files
	for path, expectedContent := range files {
		targetPath := filepath.Join(targetDir, path)
		content, err := os.ReadFile(targetPath)
		if err != nil {
			t.Errorf("failed to read %s: %v", path, err)
			continue
		}
		if diff := cmp.Diff([]byte(expectedContent), content); diff != "" {
			t.Errorf("%s: content mismatch (-want +got):\n%s", path, diff)
		}
	}
}

// TestDeltaSync tests modifying an existing file and syncing the delta
func TestDeltaSync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create initial file
	testFile := filepath.Join(sourceDir, "test.txt")
	initialContent := []byte("initial content here")
	if err := os.WriteFile(testFile, initialContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// First sync
	clientConn1, serverConn1 := net.Pipe()

	serverErr1 := make(chan error, 1)
	go func() {
		serverErr1 <- runServerSide(t, serverConn1, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn1, sourceDir, true); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	if err := <-serverErr1; err != nil {
		t.Fatalf("server failed on initial sync: %v", err)
	}

	// Modify file
	modifiedContent := []byte("modified content with more data added here")
	if err := os.WriteFile(testFile, modifiedContent, 0644); err != nil {
		t.Fatalf("failed to modify file: %v", err)
	}

	// Second sync - delta
	clientConn2, serverConn2 := net.Pipe()

	serverErr2 := make(chan error, 1)
	go func() {
		serverErr2 <- runServerSide(t, serverConn2, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn2, sourceDir, true); err != nil {
		t.Fatalf("delta sync failed: %v", err)
	}

	if err := <-serverErr2; err != nil {
		t.Fatalf("server failed on delta sync: %v", err)
	}

	// Verify updated content
	targetFile := filepath.Join(targetDir, "test.txt")
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read synced file: %v", err)
	}

	if diff := cmp.Diff(modifiedContent, content); diff != "" {
		t.Errorf("content mismatch (-want +got):\n%s", diff)
	}
}

// TestLargeFileSync tests syncing a large file (10MB)
func TestLargeFileSync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create 10MB file
	testFile := filepath.Join(sourceDir, "large.bin")
	fileSize := 10 * 1024 * 1024
	data := make([]byte, fileSize)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := os.WriteFile(testFile, data, 0644); err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}

	// Sync with larger block size
	clientConn, serverConn := net.Pipe()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runServerSide(t, serverConn, targetDir, 4096)
	}()

	if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
		t.Fatalf("client failed: %v", err)
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("server failed: %v", err)
	}

	// Verify file size
	targetFile := filepath.Join(targetDir, "large.bin")
	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("failed to stat synced file: %v", err)
	}

	if info.Size() != int64(fileSize) {
		t.Errorf("file size mismatch: got %d, want %d", info.Size(), fileSize)
	}

	// Verify full content using go-cmp
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read synced file: %v", err)
	}

	if diff := cmp.Diff(data, content); diff != "" {
		t.Errorf("content mismatch (showing first difference):\n%s", diff[:min(len(diff), 500)])
	}
}

// TestDeepDirectorySync tests deeply nested directory structures
func TestDeepDirectorySync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create 5-level nested structure
	currentDir := sourceDir
	for i := 0; i < 5; i++ {
		currentDir = filepath.Join(currentDir, "level"+string(rune('0'+i)))
		if err := os.Mkdir(currentDir, 0755); err != nil {
			t.Fatalf("failed to create nested dir: %v", err)
		}

		testFile := filepath.Join(currentDir, "file.txt")
		content := []byte("content at level " + string(rune('0'+i)))
		if err := os.WriteFile(testFile, content, 0644); err != nil {
			t.Fatalf("failed to create file at level %d: %v", i, err)
		}
	}

	// Sync
	clientConn, serverConn := net.Pipe()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runServerSide(t, serverConn, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
		t.Fatalf("client failed: %v", err)
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("server failed: %v", err)
	}

	// Verify all levels
	currentDir = targetDir
	for i := 0; i < 5; i++ {
		currentDir = filepath.Join(currentDir, "level"+string(rune('0'+i)))

		if _, err := os.Stat(currentDir); err != nil {
			t.Errorf("level %d directory not synced: %v", i, err)
		}

		testFile := filepath.Join(currentDir, "file.txt")
		content, err := os.ReadFile(testFile)
		if err != nil {
			t.Errorf("failed to read file at level %d: %v", i, err)
			continue
		}

		expectedContent := []byte("content at level " + string(rune('0'+i)))
		if diff := cmp.Diff(expectedContent, content); diff != "" {
			t.Errorf("level %d content mismatch (-want +got):\n%s", i, diff)
		}
	}
}

// TestEmptyDirectorySync tests syncing empty directories
func TestEmptyDirectorySync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create empty directory
	emptyDir := filepath.Join(sourceDir, "empty")
	if err := os.Mkdir(emptyDir, 0755); err != nil {
		t.Fatalf("failed to create empty dir: %v", err)
	}

	// Sync
	clientConn, serverConn := net.Pipe()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runServerSide(t, serverConn, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
		t.Fatalf("client failed: %v", err)
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("server failed: %v", err)
	}

	// Verify empty directory exists
	targetEmptyDir := filepath.Join(targetDir, "empty")
	info, err := os.Stat(targetEmptyDir)
	if err != nil {
		t.Fatalf("empty directory not synced: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
}

// TestBinaryFileSync tests binary files with various byte patterns
func TestBinaryFileSync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create binary file with null bytes
	testFile := filepath.Join(sourceDir, "binary.bin")
	binaryData := make([]byte, 1024)
	for i := range binaryData {
		binaryData[i] = byte(i % 256)
	}
	// Add null bytes
	for i := 100; i < 200; i++ {
		binaryData[i] = 0
	}

	if err := os.WriteFile(testFile, binaryData, 0644); err != nil {
		t.Fatalf("failed to create binary file: %v", err)
	}

	// Sync
	clientConn, serverConn := net.Pipe()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runServerSide(t, serverConn, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
		t.Fatalf("client failed: %v", err)
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("server failed: %v", err)
	}

	// Verify exact binary match using go-cmp
	targetFile := filepath.Join(targetDir, "binary.bin")
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read synced file: %v", err)
	}

	if diff := cmp.Diff(binaryData, content); diff != "" {
		t.Errorf("binary content mismatch (-want +got):\n%s", diff)
	}
}

// TestFileDeleteSync tests deleting a file and syncing the deletion
func TestFileDeleteSync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create initial files
	file1 := filepath.Join(sourceDir, "file1.txt")
	file2 := filepath.Join(sourceDir, "file2.txt")
	file3 := filepath.Join(sourceDir, "file3.txt")

	if err := os.WriteFile(file1, []byte("content 1"), 0644); err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("content 2"), 0644); err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}
	if err := os.WriteFile(file3, []byte("content 3"), 0644); err != nil {
		t.Fatalf("failed to create file3: %v", err)
	}

	// Initial sync
	clientConn1, serverConn1 := net.Pipe()

	serverErr1 := make(chan error, 1)
	go func() {
		serverErr1 <- runServerSide(t, serverConn1, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn1, sourceDir, true); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	if err := <-serverErr1; err != nil {
		t.Fatalf("server failed on initial sync: %v", err)
	}

	// Verify all files exist in target
	targetFile1 := filepath.Join(targetDir, "file1.txt")
	targetFile2 := filepath.Join(targetDir, "file2.txt")
	targetFile3 := filepath.Join(targetDir, "file3.txt")

	if _, err := os.Stat(targetFile1); err != nil {
		t.Errorf("file1 not synced: %v", err)
	}
	if _, err := os.Stat(targetFile2); err != nil {
		t.Errorf("file2 not synced: %v", err)
	}
	if _, err := os.Stat(targetFile3); err != nil {
		t.Errorf("file3 not synced: %v", err)
	}

	// Delete file2 from source
	if err := os.Remove(file2); err != nil {
		t.Fatalf("failed to delete file2: %v", err)
	}

	// Second sync with deletion enabled
	clientConn2, serverConn2 := net.Pipe()

	serverErr2 := make(chan error, 1)
	go func() {
		serverErr2 <- runServerSide(t, serverConn2, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn2, sourceDir, true); err != nil {
		t.Fatalf("deletion sync failed: %v", err)
	}

	if err := <-serverErr2; err != nil {
		t.Fatalf("server failed on deletion sync: %v", err)
	}

	// Verify file2 is deleted from target but file1 and file3 remain
	if _, err := os.Stat(targetFile1); err != nil {
		t.Error("file1 should still exist")
	}
	if _, err := os.Stat(targetFile2); !os.IsNotExist(err) {
		t.Error("file2 should be deleted from target")
	}
	if _, err := os.Stat(targetFile3); err != nil {
		t.Error("file3 should still exist")
	}
}

// TestFileTruncateSync tests truncating a file (making it smaller) and syncing
func TestFileTruncateSync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create initial file with content
	testFile := filepath.Join(sourceDir, "test.txt")
	initialContent := []byte("This is a long initial content that will be truncated later in the test")
	if err := os.WriteFile(testFile, initialContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Initial sync
	clientConn1, serverConn1 := net.Pipe()

	serverErr1 := make(chan error, 1)
	go func() {
		serverErr1 <- runServerSide(t, serverConn1, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn1, sourceDir, true); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	if err := <-serverErr1; err != nil {
		t.Fatalf("server failed on initial sync: %v", err)
	}

	// Verify initial content in target
	targetFile := filepath.Join(targetDir, "test.txt")
	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}

	if diff := cmp.Diff(initialContent, content); diff != "" {
		t.Errorf("initial content mismatch (-want +got):\n%s", diff)
	}

	// Truncate the file (make it much smaller)
	truncatedContent := []byte("Short")
	if err := os.WriteFile(testFile, truncatedContent, 0644); err != nil {
		t.Fatalf("failed to truncate file: %v", err)
	}

	// Second sync - should handle truncation
	clientConn2, serverConn2 := net.Pipe()

	serverErr2 := make(chan error, 1)
	go func() {
		serverErr2 <- runServerSide(t, serverConn2, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn2, sourceDir, true); err != nil {
		t.Fatalf("truncation sync failed: %v", err)
	}

	if err := <-serverErr2; err != nil {
		t.Fatalf("server failed on truncation sync: %v", err)
	}

	// Verify truncated content in target
	content, err = os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read target file after truncation: %v", err)
	}

	if diff := cmp.Diff(truncatedContent, content); diff != "" {
		t.Errorf("truncated content mismatch (-want +got):\n%s", diff)
	}

	// Verify size is correct (not the old size)
	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("failed to stat target file: %v", err)
	}

	if info.Size() != int64(len(truncatedContent)) {
		t.Errorf("file size mismatch: got %d, want %d", info.Size(), len(truncatedContent))
	}
}

// TestMultipleFileDeleteSync tests deleting multiple files in one sync
func TestMultipleFileDeleteSync(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create multiple files
	files := map[string]string{
		"keep1.txt":              "keep this 1",
		"delete1.txt":            "delete this 1",
		"keep2.txt":              "keep this 2",
		"delete2.txt":            "delete this 2",
		"subdir/keep3.txt":       "keep this 3",
		"subdir/delete3.txt":     "delete this 3",
	}

	for path, content := range files {
		fullPath := filepath.Join(sourceDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", path, err)
		}
	}

	// Initial sync
	clientConn1, serverConn1 := net.Pipe()

	serverErr1 := make(chan error, 1)
	go func() {
		serverErr1 <- runServerSide(t, serverConn1, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn1, sourceDir, true); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	if err := <-serverErr1; err != nil {
		t.Fatalf("server failed on initial sync: %v", err)
	}

	// Delete files from source
	filesToDelete := []string{"delete1.txt", "delete2.txt", "subdir/delete3.txt"}
	for _, path := range filesToDelete {
		fullPath := filepath.Join(sourceDir, path)
		if err := os.Remove(fullPath); err != nil {
			t.Fatalf("failed to delete %s: %v", path, err)
		}
	}

	// Second sync with deletions
	clientConn2, serverConn2 := net.Pipe()

	serverErr2 := make(chan error, 1)
	go func() {
		serverErr2 <- runServerSide(t, serverConn2, targetDir, 512)
	}()

	if err := runClientSide(t, clientConn2, sourceDir, true); err != nil {
		t.Fatalf("deletion sync failed: %v", err)
	}

	if err := <-serverErr2; err != nil {
		t.Fatalf("server failed on deletion sync: %v", err)
	}

	// Verify deleted files are gone
	for _, path := range filesToDelete {
		targetPath := filepath.Join(targetDir, path)
		if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
			t.Errorf("%s should be deleted from target", path)
		}
	}

	// Verify kept files still exist
	filesToKeep := []string{"keep1.txt", "keep2.txt", "subdir/keep3.txt"}
	for _, path := range filesToKeep {
		targetPath := filepath.Join(targetDir, path)
		if _, err := os.Stat(targetPath); err != nil {
			t.Errorf("%s should still exist: %v", path, err)
		}
	}
}

// min helper for Go versions < 1.21
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
