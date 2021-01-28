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

type client struct {
}

var (
	addr  = flag.String("addr", "127.0.0.1:33333", "server addr")
	proto = flag.String("proto", "tcp4", "connection protocol defaults to tcp (tcp, unix)")
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
	if err := run(c, flag.Arg(0)); err != nil {
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
func run(conn net.Conn, root string) error {
	defer conn.Close()
	hs := psync.NewHandshake(1, psync.WireFormatGob, 0)
	_, err := hs.WriteTo(conn)
	if err != nil {
		return err
	}
	s, err := psync.GenSrcFileList(root)
	if err != nil {
		return err
	}
	enc := gob.NewEncoder(conn)
	err = psync.SendSrcFileList(enc, s)
	if err != nil {
		return err
	}
	dec := gob.NewDecoder(conn)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := psync.RecvDstFileList(dec, s)
	if err != nil {
		return err
	}
	if n == 0 {
		log.Println("nothing has been changed")
		return nil
	}
	log.Printf("%d file(s) seems to have changed", n)
	// for _, v := range s {
	// 	fmt.Printf("%#v\n", v)
	// }
	sender := psync.Sender{
		Enc: encWriter{
			Writer:  conn,
			Encoder: enc,
		},
		Root:  root,
		Files: s,
	}
	err = sender.SendBlockDescList()
	if err != nil {
		return err
	}
	return nil
}

func die(code int, format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "psync: "+format+"\n", a...)
	os.Exit(code)
}

type encWriter struct {
	io.Writer
	psync.Encoder
}
