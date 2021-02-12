package main

import (
	"bufio"
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
	listenAddr = flag.String("listenaddr", "127.0.0.1:33333", "listen addr")
	proto      = flag.String("proto", "tcp4", "listen protocol defaults to tcp (tcp, unix)")
	blocksize  = flag.Int("blocksize", 8, "block size")

	handshakeReadDeadline        = 300 * time.Millisecond
	protoVersion          uint16 = 1
)

func main() {
	flag.Parse()
	if *proto == "unix" {
		*listenAddr = "/tmp/psyncd.sock"
		os.Remove(*listenAddr)
	}
	if flag.NArg() < 1 {
		die(1, "requires a directory argument")
	}
	if flag.NArg() != 1 {
		die(2, "invalid argument: %v", flag.Args())
	}
	ls, err := net.Listen(*proto, *listenAddr)
	if err != nil {
		die(3, "failed to listen: %v", err)
	}
	if err := run(ls, flag.Arg(0), *blocksize); err != nil {
		die(4, "%v", err)
	}
}

func run(l net.Listener, root string, blockSize int) error {
	defer l.Close()
	for {
		c, err := l.Accept()
		if err != nil {
			return fmt.Errorf("failed to accept: %w", err)
		}
		// c.SetReadDeadline(time.Now().Add(handshakeReadDeadline))
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
		br := bufio.NewReader(c)
		dec := gob.NewDecoder(br)
		enc := gob.NewEncoder(c)
		s := session{
			rcv: psync.Receiver{
				Root: root,
				Dec: decReader{
					Reader:  br,
					Decoder: dec,
				},
			},
			enc:       enc,
			dec:       dec,
			root:      root,
			blocksize: blockSize,
		}
		if err := s.syncLoop(); err != nil {
			log.Printf("session ended: %v", err)
			c.Close()
		}
	}
}

type session struct {
	rcv       psync.Receiver
	enc       psync.Encoder
	dec       psync.Decoder
	root      string
	blocksize int
}

func (c *session) syncLoop() error {
	for {
		rs, delete, err := psync.RecvSrcFileList(c.dec)
		if err != nil {
			return fmt.Errorf("src file list: %w", err)
		}
		// First remove extraneous files
		if delete {
			if err := psync.DeleteExtra(rs, c.root); err != nil {
				return err
			}
		}
		// TODO: this feels a little tricky. so find a better
		// way to sync empty directories.
		if err := psync.MkDirs(rs, c.root); err != nil {
			return err
		}
		n, err := psync.SendDstFileList(c.root, c.blocksize, rs, c.enc)
		if err != nil {
			return fmt.Errorf("send dst: %w", err)
		}
		if n == 0 {
			log.Println("nothing has been changed")
			continue
		}
		log.Printf("%d file(s) seems to have changed", n)
		err = c.rcv.BuildFiles(n, rs)
		// if err != nil {
		// 	return fmt.Errorf("build: %w", err)
		// }
		if err := c.enc.Encode(uint32(0x1a2b)); err != nil {
		}
	}
}

func die(code int, format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "psyncd: "+format+"\n", a...)
	os.Exit(code)
}

type decReader struct {
	io.Reader
	psync.Decoder
}

type debugEncoder struct {
	s []interface{}
	e *gob.Encoder
}

func (d *debugEncoder) Encode(e interface{}) error {
	d.s = append(d.s, e)
	fmt.Printf("%#v\n", e)
	return d.e.Encode(e)
}
