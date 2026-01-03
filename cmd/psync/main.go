package main

import (
	"encoding/gob"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cakturk/psync"
	"github.com/fsnotify/fsnotify"
)

var (
	addr           = flag.String("addr", "127.0.0.1:33333", "server addr")
	proto          = flag.String("proto", "tcp4", "connection protocol defaults to tcp (tcp, unix)")
	mon            = flag.Bool("mon", false, "monitor file system events")
	allowEmptyDirs = flag.Bool("allowemptydirs", true, "syncronize empty directories")
)

func main() {
	flag.Parse()
	log.SetOutput(ioutil.Discard)
	var s psync.Sender
	_ = s
	if flag.NArg() < 1 {
		die(1, "requires a directory argument")
	}
	if flag.NArg() != 1 {
		die(1, "invalid argument: %v", flag.Args())
	}
	c, err := net.DialTimeout(*proto, *addr, 200*time.Millisecond)
	_ = c
	if err != nil {
		die(1, "failed to connect %s", *addr)
	}
	var watcher *fsnotify.Watcher
	if *mon {
		watcher, err = fsnotify.NewWatcher()
		if err != nil {
			die(1, "failed to create fs watcher: %v", err)
		}
	}
	if err := run(c, flag.Arg(0), *allowEmptyDirs, watcher); err != nil {
		c.Close()
		die(2, "%v", err)
	}
}

// handshake
// generate file list
// send file list
// create sender
// receive receiver file list
// send block descriptors
// ? receive some kind of exit code, which indicates wheter
// the receiver was successful or not.
func run(conn net.Conn, root string, allowEmptyDirs bool, watcher *fsnotify.Watcher) error {
	defer conn.Close()
	hs := psync.NewHandshake(1, psync.WireFormatGob, 0)
	_, err := hs.WriteTo(conn)
	if err != nil {
		return err
	}
	lis := psync.SrcFileLister{
		Root:             root,
		IncludeEmptyDirs: allowEmptyDirs,
	}
	s, err := lis.List()
	if err != nil {
		return err
	}
	enc := gob.NewEncoder(conn)
	dec := gob.NewDecoder(conn)
	cli := client{
		sender: psync.Sender{
			Enc: encWriter{
				Writer:  conn,
				Encoder: enc,
			},
			Root: root,
		},
		enc: enc,
		dec: dec,
	}
	if err = cli.sync(s, true); err != nil {
		return err
	}
	if watcher == nil {
		return nil
	}
	defer watcher.Close()
	if err = watchDir(watcher, root); err != nil {
		return err
	}

	// Event coalescing: batch events over a time window
	const coalesceDuration = 100 * time.Millisecond
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
				if err := processFullSync(&cli, &lis); err != nil {
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
					if err := watchDirIfExists(watcher, path, &lis, &filesToSync); err != nil {
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

// processFullSync does a complete directory scan and sync
func processFullSync(cli *client, lis *psync.SrcFileLister) error {
	s, err := lis.List()
	if err != nil {
		return err
	}
	return cli.sync(s, true)
}

// watchDirIfExists adds watches for a path if it's a directory
// Handles the case where the file/dir might be deleted during processing
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

type client struct {
	mu     sync.Mutex
	sender psync.Sender
	enc    psync.Encoder
	dec    psync.Decoder
}

func (c *client) sync(list []psync.SenderSrcFile, delete bool) error {
	// Use the mutex to prevent concurrent sync operations
	c.mu.Lock()
	defer c.mu.Unlock()

	err := psync.SendSrcFileList(c.enc, list, delete)
	if err != nil {
		return err
	}
	// conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := psync.RecvDstFileList(c.dec, list)
	if err != nil {
		return err
	}
	if n == 0 {
		log.Println("nothing has been changed")
		return nil
	}
	log.Printf("%d file(s) seems to have changed", n)
	err = c.sender.SendBlockDescList(list)
	// time.Sleep(1 * time.Second)
	var ack uint32
	if err := c.dec.Decode(&ack); err != nil {
		fmt.Printf("failed to recv ack\n")
		return err
	}
	fmt.Printf("recv'd ack: %x\n", ack)
	return err
}

func die(code int, format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "psync: "+format+"\n", a...)
	os.Exit(code)
}

type encWriter struct {
	io.Writer
	psync.Encoder
}

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

func watchDir(watcher *fsnotify.Watcher, root string) error {
	return watchDirFn(watcher, root, nil)
}
