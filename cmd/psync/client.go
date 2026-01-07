package main

import (
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cakturk/psync"
	"github.com/fsnotify/fsnotify"
)

const (
	protoVersion      uint16        = 1
	coalesceDuration                = 100 * time.Millisecond
	connectTimeout                  = 200 * time.Millisecond
)

// Client handles psync protocol for sending file synchronization.
type Client struct {
	// SourceDir is the root directory to sync from
	SourceDir string

	// ServerAddr is the server address to connect to
	ServerAddr string

	// Proto is the network protocol (tcp4, tcp, unix)
	Proto string

	// AllowEmptyDirs controls whether empty directories are synced
	AllowEmptyDirs bool
}

// NewClient creates a new Client instance with the given configuration.
func NewClient(sourceDir, serverAddr, proto string, allowEmptyDirs bool) *Client {
	return &Client{
		SourceDir:      sourceDir,
		ServerAddr:     serverAddr,
		Proto:          proto,
		AllowEmptyDirs: allowEmptyDirs,
	}
}

// RunOnce performs a single synchronization with the server.
// This is the production entry point for one-shot sync.
func (c *Client) RunOnce() error {
	conn, err := net.DialTimeout(c.Proto, c.ServerAddr, connectTimeout)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", c.ServerAddr, err)
	}
	return c.SyncWithConn(conn)
}

// SyncWithConn performs a single synchronization using the provided connection.
// This method is designed for testing with net.Pipe() or other custom net.Conn implementations.
// The connection will be closed when this method returns.
func (c *Client) SyncWithConn(conn net.Conn) error {
	defer conn.Close()

	// Send handshake
	hs := psync.NewHandshake(protoVersion, psync.WireFormatGob, 0)
	if _, err := hs.WriteTo(conn); err != nil {
		return fmt.Errorf("handshake failed: %w", err)
	}

	// Generate file list
	lis := psync.SrcFileLister{
		Root:             c.SourceDir,
		IncludeEmptyDirs: c.AllowEmptyDirs,
	}
	fileList, err := lis.List()
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	// Setup encoders/decoders
	enc := gob.NewEncoder(conn)
	dec := gob.NewDecoder(conn)

	cli := &syncClient{
		sender: psync.Sender{
			Enc: encWriter{
				Writer:  conn,
				Encoder: enc,
			},
			Root: c.SourceDir,
		},
		enc: enc,
		dec: dec,
	}

	// Perform sync
	return cli.sync(fileList, true)
}

// RunMonitor starts filesystem monitoring and continuously syncs changes.
// This is the production entry point for monitor mode.
func (c *Client) RunMonitor() error {
	// Create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fs watcher: %w", err)
	}
	defer watcher.Close()

	// Connection factory for production use
	connFactory := func() (net.Conn, error) {
		return net.DialTimeout(c.Proto, c.ServerAddr, connectTimeout)
	}

	return c.MonitorWithWatcher(watcher, connFactory)
}

// MonitorWithWatcher starts filesystem monitoring using the provided watcher and connection factory.
// This method is designed for testing where you can inject a mock watcher or custom connection factory.
// The watcher will NOT be closed by this method - the caller is responsible for cleanup.
func (c *Client) MonitorWithWatcher(watcher *fsnotify.Watcher, connFactory func() (net.Conn, error)) error {
	// Create connection for initial sync
	conn, err := connFactory()
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Send handshake
	hs := psync.NewHandshake(protoVersion, psync.WireFormatGob, 0)
	if _, err := hs.WriteTo(conn); err != nil {
		conn.Close()
		return fmt.Errorf("handshake failed: %w", err)
	}

	// Setup file lister
	lis := psync.SrcFileLister{
		Root:             c.SourceDir,
		IncludeEmptyDirs: c.AllowEmptyDirs,
	}

	// Initial full sync
	fileList, err := lis.List()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to list files: %w", err)
	}

	enc := gob.NewEncoder(conn)
	dec := gob.NewDecoder(conn)

	cli := &syncClient{
		sender: psync.Sender{
			Enc: encWriter{
				Writer:  conn,
				Encoder: enc,
			},
			Root: c.SourceDir,
		},
		enc: enc,
		dec: dec,
	}

	if err = cli.sync(fileList, true); err != nil {
		conn.Close()
		return fmt.Errorf("initial sync failed: %w", err)
	}

	// Add watches for all directories
	if err = watchDir(watcher, c.SourceDir); err != nil {
		conn.Close()
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	// Start monitoring loop
	return c.monitorLoop(watcher, cli, &lis)
}

// monitorLoop handles the filesystem event monitoring and sync triggering.
func (c *Client) monitorLoop(watcher *fsnotify.Watcher, cli *syncClient, lis *psync.SrcFileLister) error {
	eventBatch := make(map[string]fsnotify.Op) // path -> last operation
	ticker := time.NewTicker(coalesceDuration)
	defer ticker.Stop()

	hasRemove := false

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// Accumulate events in batch
			// For the same path, keep the latest operation
			// Rename events come as Remove + Create
			if event.Op&fsnotify.Remove != 0 {
				hasRemove = true
				eventBatch[event.Name] = fsnotify.Remove
			} else if event.Op&fsnotify.Create != 0 {
				// If we had a remove for this path, it's a rename
				if _, wasRemoved := eventBatch[event.Name]; wasRemoved {
					// Update to Create (rename destination)
					eventBatch[event.Name] = fsnotify.Create
				} else {
					eventBatch[event.Name] = fsnotify.Create
				}
			} else if event.Op&fsnotify.Write != 0 || event.Op&fsnotify.Chmod != 0 {
				// Don't override Create with Write
				if existing, exists := eventBatch[event.Name]; !exists || existing == fsnotify.Write || existing == fsnotify.Chmod {
					eventBatch[event.Name] = fsnotify.Write
				}
			}

		case <-ticker.C:
			// Process accumulated events
			if len(eventBatch) == 0 {
				continue
			}

			// If we had remove events, do a full sync to handle deletions properly
			// This is more efficient than scanning on every individual delete
			if hasRemove {
				if err := processFullSync(cli, lis); err != nil {
					log.Printf("sync error (will retry): %v", err)
					// Don't terminate on sync errors, just log and continue
				}
				hasRemove = false
				eventBatch = make(map[string]fsnotify.Op)
				continue
			}

			// Process creates and writes
			var filesToSync []psync.SenderSrcFile
			for path, op := range eventBatch {
				if op == fsnotify.Create {
					// Handle directory creation - add watches
					if err := watchDirIfExists(watcher, path, lis, &filesToSync); err != nil {
						log.Printf("watch error for %s: %v", path, err)
						// Continue processing other files
					}
				} else if op == fsnotify.Write || op == fsnotify.Chmod {
					// Handle file modification
					sl, err := lis.AddSrcFile(nil, path)
					if err != nil {
						if os.IsNotExist(err) {
							// File was deleted after event, skip it
							continue
						}
						log.Printf("error adding file %s: %v", path, err)
						continue
					}
					filesToSync = append(filesToSync, sl...)
				}
			}

			if len(filesToSync) > 0 {
				if err := cli.sync(filesToSync, false); err != nil {
					log.Printf("sync error (will retry): %v", err)
					// Don't terminate on sync errors
				}
			}

			// Clear batch for next window
			eventBatch = make(map[string]fsnotify.Op)

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			// Log watcher errors but don't terminate
			log.Printf("watcher error: %v", err)
		}
	}
}

// syncClient handles the sync protocol operations.
type syncClient struct {
	mu     sync.Mutex
	sender psync.Sender
	enc    psync.Encoder
	dec    psync.Decoder
}

// sync performs the sync protocol for the given file list.
func (c *syncClient) sync(list []psync.SenderSrcFile, delete bool) error {
	// Use the mutex to prevent concurrent sync operations
	c.mu.Lock()
	defer c.mu.Unlock()

	// Send source file list
	err := psync.SendSrcFileList(c.enc, list, delete)
	if err != nil {
		return fmt.Errorf("send src file list: %w", err)
	}

	// Receive destination file list
	n, err := psync.RecvDstFileList(c.dec, list)
	if err != nil {
		return fmt.Errorf("recv dst file list: %w", err)
	}

	if n == 0 {
		log.Println("nothing has been changed")
		return nil
	}

	log.Printf("%d file(s) seems to have changed", n)

	// Send block descriptors
	err = c.sender.SendBlockDescList(list)
	if err != nil {
		return fmt.Errorf("send block descriptors: %w", err)
	}

	// Receive acknowledgment
	var ack uint32
	if err := c.dec.Decode(&ack); err != nil {
		return fmt.Errorf("failed to recv ack: %w", err)
	}

	fmt.Printf("recv'd ack: %x\n", ack)
	return nil
}

// encWriter combines an io.Writer with an Encoder for the Sender.
type encWriter struct {
	io.Writer
	psync.Encoder
}

// processFullSync does a complete directory scan and sync.
func processFullSync(cli *syncClient, lis *psync.SrcFileLister) error {
	s, err := lis.List()
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	return cli.sync(s, true)
}

// watchDirIfExists adds watches for a path if it's a directory.
// Handles the case where the file/dir might be deleted during processing.
func watchDirIfExists(watcher *fsnotify.Watcher, path string, lis *psync.SrcFileLister, files *[]psync.SenderSrcFile) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File was deleted, not an error
			return nil
		}
		return err
	}

	// Add to file list
	sl, err := lis.AddSrcFile(*files, path)
	if err != nil {
		return err
	}
	*files = sl

	// If it's a directory, walk it and add watches
	if info.IsDir() {
		err = filepath.Walk(path, func(walkPath string, fi os.FileInfo, err error) error {
			if err != nil {
				// Handle errors during walk (file disappeared)
				if os.IsNotExist(err) {
					return nil // Skip missing files
				}
				return nil // Continue on other errors
			}

			// Add to file list
			sl, err := lis.AddSrcFile(*files, walkPath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil // File disappeared, skip it
				}
				return nil // Continue on other errors
			}
			*files = sl

			if fi.IsDir() {
				// Add watch, but don't fail if it errors
				if err = watcher.Add(walkPath); err != nil {
					log.Printf("failed to add watch for %s: %v", walkPath, err)
				}
			}
			return nil
		})
		return err
	}
	return nil
}

// watchDirFn walks a directory tree and adds watches, optionally calling fn for each path.
func watchDirFn(watcher *fsnotify.Watcher, root string, fn func(path string)) error {
	err := filepath.Walk(root, func(walkPath string, fi os.FileInfo, err error) error {
		if err != nil {
			// Handle errors during walk gracefully
			if os.IsNotExist(err) {
				return nil // Skip files that disappeared
			}
			return nil // Continue on other errors
		}
		if fn != nil {
			fn(walkPath)
		}
		if fi.IsDir() {
			if err = watcher.Add(walkPath); err != nil {
				log.Printf("failed to add watch for %s: %v", walkPath, err)
				return nil // Don't abort on watch errors
			}
		}
		return nil
	})
	return err
}

// watchDir adds watches for all directories under root.
func watchDir(watcher *fsnotify.Watcher, root string) error {
	return watchDirFn(watcher, root, nil)
}
