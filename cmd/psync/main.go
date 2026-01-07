package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
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

	if flag.NArg() < 1 {
		die(1, "requires a directory argument")
	}
	if flag.NArg() != 1 {
		die(1, "invalid argument: %v", flag.Args())
	}

	// Create client
	client := NewClient(flag.Arg(0), *addr, *proto, *allowEmptyDirs)

	// Run in appropriate mode
	var err error
	if *mon {
		err = client.RunMonitor()
	} else {
		err = client.RunOnce()
	}

	if err != nil {
		die(2, "%v", err)
	}
}

func die(code int, format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "psync: "+format+"\n", a...)
	os.Exit(code)
}
