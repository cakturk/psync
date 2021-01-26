package main

import (
	"encoding/gob"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/cakturk/psync"
)

var (
	addr  = flag.String("addr", "127.0.0.1:33333", "listen addr")
	proto = flag.String("proto", "tcp4", "listen protocol defaults to tcp (tcp, unix)")

	handshakeReadDeadline        = 300 * time.Millisecond
	protoVersion          uint16 = 1
)

func main() {
	flag.Parse()
	if *proto == "unix" {
		*addr = "/tmp/psyncd.sock"
		os.Remove(*addr)
	}
	if flag.NArg() < 1 {
		die(1, "requires a directory argument")
	}
	if flag.NArg() != 1 {
		die(1, "invalid argument: %v", flag.Args())
	}
	ls, err := net.Listen(*proto, *addr)
	if err != nil {
		die(1, "failed to listen: %v", err)
	}
	if err := run(ls, flag.Arg(0)); err != nil {
		die(1, "%v", err)
	}
}

func run(l net.Listener, root string) error {
	defer l.Close()
	for {
		c, err := l.Accept()
		if err != nil {
			return fmt.Errorf("failed to accept: %w", err)
		}
		c.SetReadDeadline(time.Now().Add(handshakeReadDeadline))
		h, err := psync.ReadHandshake(c)
		if err != nil {
			c.Close()
			if os.IsTimeout(err) {
				continue
			}
			return fmt.Errorf("failed to handshake: %w", err)
		}
		if !h.Valid() {
			log.Print("invalid protocol header")
			c.Close()
			continue
		}
		if h.Version != protoVersion {
			log.Print("protocol version mismatch")
			c.Close()
			continue
		}
		dec := gob.NewDecoder(c)
		rs, err := psync.RecvSrcFileList(dec)
		if err != nil {
			return err
		}
		enc := gob.NewEncoder(c)
		n, err := psync.SendDstFileList(root, 512, rs, enc)
		if err != nil {
			return err
		}
		log.Printf("%d file(s) seems to have changed", n)
	}
}

func die(code int, format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "psyncd: "+format+"\n", a...)
	os.Exit(code)
}
