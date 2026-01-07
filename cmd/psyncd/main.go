package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
)

var (
	listenAddr = flag.String("listenaddr", "127.0.0.1:33333", "listen addr")
	proto      = flag.String("proto", "tcp4", "listen protocol defaults to tcp (tcp, unix)")
	blocksize  = flag.Int("blocksize", 8, "block size")
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

	// Create server instance
	server := NewServer(flag.Arg(0), *blocksize)

	// Setup listener
	ls, err := net.Listen(*proto, *listenAddr)
	if err != nil {
		die(3, "failed to listen: %v", err)
	}

	// Run server
	if err := run(ls, server); err != nil {
		die(4, "%v", err)
	}
}

// run handles the main accept loop, delegating connection handling to the server.
func run(l net.Listener, server *Server) error {
	defer l.Close()
	for {
		conn, err := l.Accept()
		if err != nil {
			return fmt.Errorf("failed to accept: %w", err)
		}

		// Handle each connection in a goroutine
		go func(c net.Conn) {
			if err := server.HandleConnection(c); err != nil {
				log.Printf("connection handler error: %v", err)
			}
		}(conn)
	}
}

func die(code int, format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "psyncd: "+format+"\n", a...)
	os.Exit(code)
}
