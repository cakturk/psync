package main

import (
	"encoding/gob"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/cakturk/psync"
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
	if err := run(c, flag.Arg(0), *allowEmptyDirs, *mon); err != nil {
		c.Close()
		die(2, "psync: %v", err)
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
func run(conn net.Conn, root string, allowEmptyDirs, mon bool) error {
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
	err = cli.sync(s, true)
	if mon {
		return err
	}
	return nil
}

type client struct {
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
