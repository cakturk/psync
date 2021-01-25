package main

import (
	"flag"
	"fmt"
	"io"
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

	c, err := net.DialTimeout(*proto, *addr, 200*time.Millisecond)
	_ = c
	if err != nil {
		die(1, "failed to connect %s", *addr)
	}
	if err := run(c); err != nil {
		c.Close()
		die(2, "psync: %v", err)
	}
}

func run(rw io.ReadWriter) error {
	// handshake
	// generate file list
	// send file list
	// create sender
	// receive receiver file list
	// send block descriptors
	// ? receive some kind of exit code, which indicates wheter
	// the receiver was successful or not.
	hs := psync.NewHandshake(1, psync.WireFormatGob, 0)
	_, err := hs.WriteTo(rw)
	if err != nil {
		return err
	}
	return nil
}

func die(code int, format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "psync: "+format+"\n", a...)
	os.Exit(code)
}
