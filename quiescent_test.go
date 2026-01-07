package psync_test

import (
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// ChurnGenerator represents a function that generates filesystem churn
type ChurnGenerator func(dir string, stop <-chan struct{}, wg *sync.WaitGroup)

// churnCompilePattern simulates C compiler object file creation pattern:
// - Create temp file with random content
// - Rename to final name (atomic)
// - Occasionally delete old files
func churnCompilePattern(dir string, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	subdir := filepath.Join(dir, "build")
	os.MkdirAll(subdir, 0755)

	counter := 0
	ticker := time.NewTicker(50 * time.Millisecond) // 20 ops/sec
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			filename := fmt.Sprintf("file_%d.o", counter)
			tempname := fmt.Sprintf(".%s.tmp", filename)

			// Write to temp file
			tempPath := filepath.Join(subdir, tempname)
			data := make([]byte, 100+rand.Intn(900))
			rand.Read(data)
			os.WriteFile(tempPath, data, 0644)

			// Atomic rename
			finalPath := filepath.Join(subdir, filename)
			os.Rename(tempPath, finalPath)

			counter++

			// Occasionally delete old files
			if counter%20 == 0 {
				entries, _ := os.ReadDir(subdir)
				for i := 0; i < 5 && i < len(entries); i++ {
					os.Remove(filepath.Join(subdir, entries[i].Name()))
				}
			}
		}
	}
}

// churnRapidSmallFiles simulates rapid small file creation/deletion
func churnRapidSmallFiles(dir string, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	subdir := filepath.Join(dir, "temp")
	os.MkdirAll(subdir, 0755)

	ticker := time.NewTicker(100 * time.Millisecond) // 10 ops/sec
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Burst create
			for i := 0; i < 5; i++ {
				filename := filepath.Join(subdir, fmt.Sprintf("temp%d.txt", i))
				os.WriteFile(filename, []byte(fmt.Sprintf("data %d", i)), 0644)
			}

			// Burst delete
			time.Sleep(50 * time.Millisecond)
			entries, _ := os.ReadDir(subdir)
			for _, entry := range entries {
				os.Remove(filepath.Join(subdir, entry.Name()))
			}
		}
	}
}

// churnAppendPattern simulates log file append operations
func churnAppendPattern(dir string, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	subdir := filepath.Join(dir, "logs")
	os.MkdirAll(subdir, 0755)

	logfile := filepath.Join(subdir, "build.log")
	f, _ := os.OpenFile(logfile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	defer f.Close()

	ticker := time.NewTicker(50 * time.Millisecond) // 20 ops/sec
	defer ticker.Stop()

	counter := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			fmt.Fprintf(f, "Log entry %d at %s\n", counter, time.Now().Format(time.RFC3339Nano))
			counter++

			// Occasionally truncate log
			if counter%100 == 0 {
				f.Truncate(0)
				f.Seek(0, 0)
			}
		}
	}
}

// runChurnForDuration runs a churn generator for specified duration
func runChurnForDuration(t *testing.T, dir string, generator ChurnGenerator, duration time.Duration) {
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go generator(dir, stop, &wg)

	time.Sleep(duration)
	close(stop)
	wg.Wait()
}

// compareDirectories compares two directories and returns differences
func compareDirectories(t *testing.T, sourceDir, targetDir string) []string {
	t.Helper()

	var diffs []string

	// Walk source directory
	err := filepath.Walk(sourceDir, func(srcPath string, srcInfo os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(sourceDir, srcPath)
		targetPath := filepath.Join(targetDir, relPath)

		// Check if exists in target
		targetInfo, err := os.Stat(targetPath)
		if err != nil {
			if os.IsNotExist(err) {
				diffs = append(diffs, fmt.Sprintf("missing in target: %s", relPath))
				return nil
			}
			return err
		}

		// Compare file vs directory
		if srcInfo.IsDir() != targetInfo.IsDir() {
			diffs = append(diffs, fmt.Sprintf("type mismatch: %s", relPath))
			return nil
		}

		// If file, compare content
		if !srcInfo.IsDir() {
			srcContent, _ := os.ReadFile(srcPath)
			targetContent, _ := os.ReadFile(targetPath)

			if !cmp.Equal(srcContent, targetContent) {
				diffs = append(diffs, fmt.Sprintf("content mismatch: %s (size: %d vs %d)",
					relPath, len(srcContent), len(targetContent)))
			}
		}

		return nil
	})

	if err != nil {
		t.Fatalf("failed to walk source directory: %v", err)
	}

	// Check for extra files in target
	err = filepath.Walk(targetDir, func(targetPath string, targetInfo os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(targetDir, targetPath)
		srcPath := filepath.Join(sourceDir, relPath)

		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			diffs = append(diffs, fmt.Sprintf("extra in target: %s", relPath))
		}

		return nil
	})

	if err != nil {
		t.Fatalf("failed to walk target directory: %v", err)
	}

	return diffs
}

// TestQuiescentBasic tests basic quiescent period verification with simple churn
func TestQuiescentBasic(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Create initial files
	initialFiles := map[string]string{
		"file1.txt": "initial content 1",
		"file2.txt": "initial content 2",
	}

	for path, content := range initialFiles {
		fullPath := filepath.Join(sourceDir, path)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create initial file %s: %v", path, err)
		}
	}

	// Run 3 cycles of churn-sync-verify
	cycles := 3
	for cycle := 0; cycle < cycles; cycle++ {
		t.Logf("Cycle %d/%d: Starting churn phase", cycle+1, cycles)

		// CHURN PHASE: Create/modify files for 1 second
		runChurnForDuration(t, sourceDir, churnCompilePattern, 1*time.Second)

		t.Logf("Cycle %d/%d: Churn stopped, waiting for settle", cycle+1, cycles)

		// QUIESCENT PHASE: Wait a bit for any final writes
		time.Sleep(100 * time.Millisecond)

		t.Logf("Cycle %d/%d: Running sync", cycle+1, cycles)

		// SYNC PHASE
		clientConn, serverConn := net.Pipe()

		serverErr := make(chan error, 1)
		go func() {
			serverErr <- runServerSide(t, serverConn, targetDir, 512)
		}()

		if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
			t.Fatalf("cycle %d: sync failed: %v", cycle, err)
		}

		if err := <-serverErr; err != nil {
			t.Fatalf("cycle %d: server failed: %v", cycle, err)
		}

		t.Logf("Cycle %d/%d: Verifying directories match", cycle+1, cycles)

		// VERIFY PHASE
		diffs := compareDirectories(t, sourceDir, targetDir)
		if len(diffs) > 0 {
			t.Errorf("cycle %d: directories differ:\n", cycle)
			for _, diff := range diffs {
				t.Errorf("  - %s", diff)
			}
		} else {
			t.Logf("Cycle %d/%d: ✓ Verified - directories match", cycle+1, cycles)
		}
	}
}

// TestQuiescentMultiplePatterns tests multiple churn patterns in parallel
func TestQuiescentMultiplePatterns(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Run 5 cycles with multiple churn patterns
	cycles := 5
	for cycle := 0; cycle < cycles; cycle++ {
		t.Logf("Cycle %d/%d: Starting multi-pattern churn", cycle+1, cycles)

		// CHURN PHASE: Run multiple patterns in parallel
		stop := make(chan struct{})
		var wg sync.WaitGroup

		patterns := []ChurnGenerator{
			churnCompilePattern,
			churnRapidSmallFiles,
			churnAppendPattern,
		}

		for _, pattern := range patterns {
			wg.Add(1)
			go pattern(sourceDir, stop, &wg)
		}

		// Let churn run for 2 seconds
		time.Sleep(2 * time.Second)
		close(stop)
		wg.Wait()

		t.Logf("Cycle %d/%d: All churn stopped, settling", cycle+1, cycles)

		// QUIESCENT PHASE
		time.Sleep(200 * time.Millisecond)

		// SYNC PHASE
		clientConn, serverConn := net.Pipe()

		serverErr := make(chan error, 1)
		go func() {
			serverErr <- runServerSide(t, serverConn, targetDir, 512)
		}()

		if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
			t.Fatalf("cycle %d: sync failed: %v", cycle, err)
		}

		if err := <-serverErr; err != nil {
			t.Fatalf("cycle %d: server failed: %v", cycle, err)
		}

		// VERIFY PHASE
		diffs := compareDirectories(t, sourceDir, targetDir)
		if len(diffs) > 0 {
			t.Errorf("cycle %d: directories differ after multi-pattern churn:", cycle)
			for i, diff := range diffs {
				if i < 10 { // Limit output
					t.Errorf("  - %s", diff)
				}
			}
			if len(diffs) > 10 {
				t.Errorf("  ... and %d more differences", len(diffs)-10)
			}
		} else {
			t.Logf("Cycle %d/%d: ✓ Verified - directories match", cycle+1, cycles)
		}
	}
}

// TestQuiescentLongRunning tests sustained churn over longer period
func TestQuiescentLongRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	// Run 10 cycles with longer churn periods
	cycles := 10
	var totalOps atomic.Int64

	for cycle := 0; cycle < cycles; cycle++ {
		t.Logf("Cycle %d/%d: Starting sustained churn", cycle+1, cycles)

		// CHURN PHASE: 5 seconds of heavy churn
		stop := make(chan struct{})
		var wg sync.WaitGroup

		// Track operations
		opsBefore := totalOps.Load()

		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()

			subdir := filepath.Join(sourceDir, "sustained")
			os.MkdirAll(subdir, 0755)

			counter := 0
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					// Create/modify/delete operations
					filename := filepath.Join(subdir, fmt.Sprintf("file_%d.dat", counter%100))
					os.WriteFile(filename, []byte(fmt.Sprintf("data %d", counter)), 0644)
					totalOps.Add(1)

					if counter%20 == 0 {
						// Delete some files
						os.Remove(filepath.Join(subdir, fmt.Sprintf("file_%d.dat", (counter-10)%100)))
					}

					counter++
				}
			}
		}()

		time.Sleep(5 * time.Second)
		close(stop)
		wg.Wait()

		opsThisCycle := totalOps.Load() - opsBefore
		t.Logf("Cycle %d/%d: Completed %d operations", cycle+1, cycles, opsThisCycle)

		// QUIESCENT PHASE
		time.Sleep(200 * time.Millisecond)

		// SYNC PHASE
		clientConn, serverConn := net.Pipe()

		serverErr := make(chan error, 1)
		go func() {
			serverErr <- runServerSide(t, serverConn, targetDir, 512)
		}()

		if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
			t.Fatalf("cycle %d: sync failed: %v", cycle, err)
		}

		if err := <-serverErr; err != nil {
			t.Fatalf("cycle %d: server failed: %v", cycle, err)
		}

		// VERIFY PHASE
		diffs := compareDirectories(t, sourceDir, targetDir)
		if len(diffs) > 0 {
			t.Errorf("cycle %d: directories differ after sustained churn:", cycle)
			for i, diff := range diffs {
				if i < 5 {
					t.Errorf("  - %s", diff)
				}
			}
			if len(diffs) > 5 {
				t.Errorf("  ... and %d more differences", len(diffs)-5)
			}
			t.FailNow()
		}

		t.Logf("Cycle %d/%d: ✓ Verified - directories match", cycle+1, cycles)
	}

	t.Logf("Test completed: %d total operations across %d cycles", totalOps.Load(), cycles)
}

// TestQuiescentFileCount verifies file count tracking during churn
func TestQuiescentFileCount(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	cycles := 5
	for cycle := 0; cycle < cycles; cycle++ {
		// CHURN PHASE
		runChurnForDuration(t, sourceDir, churnCompilePattern, 2*time.Second)

		// Count files in source
		var sourceCount int
		filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				sourceCount++
			}
			return nil
		})

		t.Logf("Cycle %d: Source has %d files", cycle+1, sourceCount)

		// QUIESCENT PHASE
		time.Sleep(100 * time.Millisecond)

		// SYNC PHASE
		clientConn, serverConn := net.Pipe()

		serverErr := make(chan error, 1)
		go func() {
			serverErr <- runServerSide(t, serverConn, targetDir, 512)
		}()

		if err := runClientSide(t, clientConn, sourceDir, true); err != nil {
			t.Fatalf("cycle %d: sync failed: %v", cycle, err)
		}

		if err := <-serverErr; err != nil {
			t.Fatalf("cycle %d: server failed: %v", cycle, err)
		}

		// Count files in target
		var targetCount int
		filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				targetCount++
			}
			return nil
		})

		t.Logf("Cycle %d: Target has %d files", cycle+1, targetCount)

		// VERIFY counts match
		if sourceCount != targetCount {
			t.Errorf("cycle %d: file count mismatch: source=%d, target=%d",
				cycle, sourceCount, targetCount)
		}

		// VERIFY content matches
		diffs := compareDirectories(t, sourceDir, targetDir)
		if len(diffs) > 0 {
			t.Errorf("cycle %d: found %d differences", cycle, len(diffs))
		} else {
			t.Logf("Cycle %d: ✓ Verified - %d files match", cycle+1, sourceCount)
		}
	}
}
