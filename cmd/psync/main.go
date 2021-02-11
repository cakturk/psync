package main

import (
	"encoding/gob"
	"flag"
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

var (
	addr           = flag.String("addr", "127.0.0.1:33333", "server addr")
	proto          = flag.String("proto", "tcp4", "connection protocol defaults to tcp (tcp, unix)")
	deleteExtra    = flag.Bool("delete", false, "delete extraneous files from dest dirs")
	mon            = flag.Bool("mon", false, "monitor file system events")
	allowEmptyDirs = flag.Bool("allowemptydirs", true, "syncronize empty directories")
)

func main() {
	flag.Parse()
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
	if _, err = watchRecursive(watcher, root); err != nil {
		return err
	}
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			log.Println("event:", event)
			switch event.Op {
			case fsnotify.Write, fsnotify.Chmod:
				sl, err := lis.AddSrcFile(nil, event.Name)
				if err != nil {
					return err
				}
				if err := cli.sync(sl, false); err != nil {
					return err
				}
				log.Print("write|modify:", event, sl)
			case fsnotify.Create:
				var sl []psync.SenderSrcFile
				_, err = watchRecursiveFn(watcher, event.Name, func(path string) {
					// log.Printf("cb: %s", path)
					sl, err = lis.AddSrcFile(sl, path)
					if err != nil {
						return
					}
				})
				if err != nil {
					return err
				}
				if err := cli.sync(sl, false); err != nil {
					return err
				}
				log.Print("create:", event, s, sl)
			case fsnotify.Remove:
				s, err := lis.List()
				if err != nil {
					return err
				}
				log.Printf("begin")
				err = cli.sync(s, true)
				log.Printf("end")
				if err != nil {
					return err
				}
				log.Print("remove:", event, s)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Println("error:", err)
		}
	}
}

type client struct {
	mu     sync.Mutex
	sender psync.Sender
	enc    psync.Encoder
	dec    psync.Decoder
}

func (c *client) sync(list []psync.SenderSrcFile, delete bool) error {
	log.Printf("sync: 0")
	c.mu.Lock()
	defer c.mu.Unlock()
	log.Printf("sync: 1")
	err := psync.SendSrcFileList(c.enc, list, delete)
	if err != nil {
		return err
	}
	// conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	log.Printf("sync: 2")
	n, err := psync.RecvDstFileList(c.dec, list)
	if err != nil {
		return err
	}
	if n == 0 {
		log.Println("nothing has been changed")
		return nil
	}
	log.Printf("sync: 3")
	log.Printf("%d file(s) seems to have changed", n)
	return c.sender.SendBlockDescList(list)
}

func die(code int, format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "psync: "+format+"\n", a...)
	os.Exit(code)
}

type encWriter struct {
	io.Writer
	psync.Encoder
}

func watchRecursiveFn(watcher *fsnotify.Watcher, root string, fn func(path string)) ([]string, error) {
	var s []string
	err := filepath.Walk(root, func(walkPath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// log.Printf("walk: %s, isdir: %v", walkPath, fi.IsDir())
		if fn != nil {
			fn(walkPath)
		}
		s = append(s, walkPath)
		if fi.IsDir() {
			// log.Printf("rec: %s, dir: %v", path, fi.IsDir())
			if err = watcher.Add(walkPath); err != nil {
				return err
			}
		}
		return nil
	})
	return s, err
}

func watchRecursive(watcher *fsnotify.Watcher, root string) ([]string, error) {
	return watchRecursiveFn(watcher, root, nil)
}
