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
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			fmt.Println("event:", event)
			switch event.Op {
			case fsnotify.Write, fsnotify.Chmod:
				sl, err := lis.AddSrcFile(nil, event.Name)
				if err != nil {
					return err
				}
				if err := cli.sync(sl, false); err != nil {
					return err
				}
			case fsnotify.Create:
				var sl []psync.SenderSrcFile
				err = watchDirFn(watcher, event.Name, func(path string) {
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
			case fsnotify.Remove:
				s, err := lis.List()
				if err != nil {
					return err
				}
				err = cli.sync(s, true)
				if err != nil {
					return err
				}
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
			return err
		}
		if fn != nil {
			fn(walkPath)
		}
		if fi.IsDir() {
			if err = watcher.Add(walkPath); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func watchDir(watcher *fsnotify.Watcher, root string) error {
	return watchDirFn(watcher, root, nil)
}
