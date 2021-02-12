psync - dummy directory syncronization tool
===========================================

This package is still work-in-progress. Feedback is always welcome!

How to build
------------

- Build client

go build ./cmd/psync

- Build server

go build ./cmd/psyncd

Basic usage
-----------

Usage of ./psyncd:
  -blocksize int
        block size (default 8)
  -listenaddr string
        listen addr (default "127.0.0.1:33333")
  -proto string
        listen protocol defaults to tcp (tcp, unix) (default "tcp4")


Usage of ./psync:
  -addr string
    	server addr (default "127.0.0.1:33333")
  -allowemptydirs
    	syncronize empty directories (default true)
  -mon
    	monitor file system events
  -proto string
    	connection protocol defaults to tcp (tcp, unix) (default "tcp4")


Example:

Type the following command to start the server

$ ./psyncd -blocksize 512 -listenaddr 127.0.0.1:33333 /path/to/serverdir

In another terminal window:

Type the following command to do a simple one-off sync.

$ ./psync /path/to/clientdir

If you want psync to watch the client directory for file-system
changes, you can pass the "-mon" flag.

$ ./psync -mon /path/to/clientdir
