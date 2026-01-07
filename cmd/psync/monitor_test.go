package main

import (
	"bufio"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cakturk/psync"
	"github.com/fsnotify/fsnotify"
)

// decReader combines an io.Reader with a Decoder for the Receiver
type decReader struct {
	io.Reader
	psync.Decoder
}

// Helper to run server side - handles multiple syncs in a loop (like real server)
func runTestServerSide(t *testing.T, conn net.Conn, targetDir string) error {
	defer conn.Close()

	// Read handshake
	h, err := psync.ReadHandshake(conn)
	if err != nil {
		return err
	}
	if !h.Valid() {
		t.Error("invalid handshake")
		return fmt.Errorf("invalid handshake")
	}

	// Setup encoder/decoder with buffering
	br := bufio.NewReader(conn)
	dec := gob.NewDecoder(br)
	enc := gob.NewEncoder(conn)

	// Sync loop - handle multiple syncs like real server
	for {
		// Receive source file list
		srcFiles, deleteExtra, err := psync.RecvSrcFileList(dec)
		if err != nil {
			// Connection closed or error - exit loop
			if err == io.EOF {
				return nil
			}
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
		n, err := psync.SendDstFileList(targetDir, 512, srcFiles, enc)
		if err != nil {
			return err
		}

		if n == 0 {
			// No files changed, continue to next sync
			continue
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
			// Don't fail on build errors, just continue
			// (matches server behavior)
		}

		// Send acknowledgment
		if err := enc.Encode(uint32(0x1a2b)); err != nil {
			return err
		}
	}
}

// TestMonitorModeBasic tests basic monitor mode with real fsnotify and proper cleanup
func TestMonitorModeBasic(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create initial files
	initialFiles := map[string]string{
		"file1.txt": "initial 1",
		"file2.txt": "initial 2",
	}
	for path, content := range initialFiles {
		fullPath := filepath.Join(sourceDir, path)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create initial file: %v", err)
		}
	}

	// Setup context with timeout (safety net + cancellation)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup net.Pipe connection
	clientConn, serverConn := net.Pipe()

	// Start server
	serverDone := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		serverDone <- runTestServerSide(t, serverConn, targetDir)
	}()
	defer func() {
		clientConn.Close()
		<-serverDone
	}()

	// Setup client with real fsnotify
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	// Start monitor mode in background
	client := NewClient(sourceDir, "unused", "unused", true)

	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- client.MonitorWithWatcher(ctx, watcher, func() (net.Conn, error) {
			return clientConn, nil
		})
	}()
	defer func() {
		cancel() // Stop monitor
		err := <-monitorDone
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			t.Errorf("monitor mode error: %v", err)
		}
	}()

	// Wait for initial sync
	time.Sleep(500 * time.Millisecond)

	// Run 3 file creation cycles
	for cycle := 0; cycle < 3; cycle++ {
		t.Logf("Cycle %d: Creating file", cycle+1)

		// Create a new file
		newFile := filepath.Join(sourceDir, fmt.Sprintf("cycle_%d.txt", cycle))
		content := []byte(fmt.Sprintf("content from cycle %d", cycle))
		if err := os.WriteFile(newFile, content, 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		// Wait for fsnotify + sync
		time.Sleep(400 * time.Millisecond)

		t.Logf("Cycle %d: Verifying", cycle+1)

		// Verify file exists in target
		targetFile := filepath.Join(targetDir, filepath.Base(newFile))
		targetContent, err := os.ReadFile(targetFile)
		if err != nil {
			t.Errorf("cycle %d: file not synced: %v", cycle, err)
		} else if string(targetContent) != string(content) {
			t.Errorf("cycle %d: content mismatch", cycle)
		} else {
			t.Logf("Cycle %d: ✓ Verified", cycle+1)
		}
	}
}

// TestMonitorModeFileOperations tests various file operations
func TestMonitorModeFileOperations(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Setup context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup connection
	clientConn, serverConn := net.Pipe()

	// Start server
	serverDone := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		serverDone <- runTestServerSide(t, serverConn, targetDir)
	}()
	defer func() {
		clientConn.Close()
		<-serverDone
	}()

	// Setup fsnotify
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	// Start monitor mode
	client := NewClient(sourceDir, "unused", "unused", true)

	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- client.MonitorWithWatcher(ctx, watcher, func() (net.Conn, error) {
			return clientConn, nil
		})
	}()
	defer func() {
		cancel()
		err := <-monitorDone
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			t.Errorf("monitor mode error: %v", err)
		}
	}()

	// Wait for initial sync
	time.Sleep(300 * time.Millisecond)

	testFile := filepath.Join(sourceDir, "test.txt")
	targetFile := filepath.Join(targetDir, "test.txt")

	// Test 1: Create file
	t.Log("Test 1: Creating file")
	if err := os.WriteFile(testFile, []byte("initial content"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Errorf("file not synced: %v", err)
	} else if string(content) != "initial content" {
		t.Errorf("content mismatch: got %q, want %q", content, "initial content")
	} else {
		t.Log("Test 1: ✓ File created and synced")
	}

	// Test 2: Modify file
	t.Log("Test 2: Modifying file")
	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatalf("failed to modify file: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	content, err = os.ReadFile(targetFile)
	if err != nil {
		t.Errorf("modified file not synced: %v", err)
	} else if string(content) != "modified content" {
		t.Errorf("content mismatch: got %q, want %q", content, "modified content")
	} else {
		t.Log("Test 2: ✓ File modification synced")
	}

	// Test 3: Truncate file
	t.Log("Test 3: Truncating file")
	if err := os.WriteFile(testFile, []byte("short"), 0644); err != nil {
		t.Fatalf("failed to truncate file: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	content, err = os.ReadFile(targetFile)
	if err != nil {
		t.Errorf("truncated file not synced: %v", err)
	} else if string(content) != "short" {
		t.Errorf("content mismatch: got %q, want %q", content, "short")
	} else {
		t.Log("Test 3: ✓ File truncation synced")
	}

	// Test 4: Delete file
	t.Log("Test 4: Deleting file")
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to delete file: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
		t.Error("deleted file still exists in target")
	} else {
		t.Log("Test 4: ✓ File deletion synced")
	}
}

// TestMonitorModeHeavyChurn tests monitor mode under heavy churn
func TestMonitorModeHeavyChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy churn test in short mode")
	}

	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Setup context
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Setup connection
	clientConn, serverConn := net.Pipe()

	// Start server
	serverDone := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		serverDone <- runTestServerSide(t, serverConn, targetDir)
	}()
	defer func() {
		clientConn.Close()
		<-serverDone
	}()

	// Setup fsnotify
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	// Start monitor mode
	client := NewClient(sourceDir, "unused", "unused", true)

	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- client.MonitorWithWatcher(ctx, watcher, func() (net.Conn, error) {
			return clientConn, nil
		})
	}()
	defer func() {
		cancel()
		err := <-monitorDone
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			t.Errorf("monitor mode error: %v", err)
		}
	}()

	// Wait for initial sync
	time.Sleep(500 * time.Millisecond)

	// Heavy churn: rapid file operations for 5 seconds
	t.Log("Starting heavy churn (5 seconds)")
	stopChurn := make(chan struct{})
	churnDone := make(chan struct{})

	go func() {
		defer close(churnDone)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()

		subdir := filepath.Join(sourceDir, "heavy")
		os.MkdirAll(subdir, 0755)

		counter := 0
		for {
			select {
			case <-stopChurn:
				return
			case <-ticker.C:
				filename := filepath.Join(subdir, fmt.Sprintf("file_%d.dat", counter%50))
				data := make([]byte, 100+rand.Intn(900))
				rand.Read(data)
				os.WriteFile(filename, data, 0644)

				if counter%10 == 0 {
					os.Remove(filepath.Join(subdir, fmt.Sprintf("file_%d.dat", (counter-5)%50)))
				}

				counter++
			}
		}
	}()

	time.Sleep(5 * time.Second)
	close(stopChurn)
	<-churnDone

	t.Log("Heavy churn stopped, waiting for sync")
	time.Sleep(2 * time.Second)

	t.Log("Verifying files exist in target")

	// Verify target directory exists and has files
	targetSubdir := filepath.Join(targetDir, "heavy")
	entries, err := os.ReadDir(targetSubdir)
	if err != nil {
		t.Errorf("target directory not found: %v", err)
	} else {
		t.Logf("✓ Target has %d files after heavy churn", len(entries))
	}
}
