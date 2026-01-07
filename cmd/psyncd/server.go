package main

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/cakturk/psync"
)

const (
	handshakeReadDeadline        = 300 * time.Millisecond
	protoVersion          uint16 = 1
)

// Server handles psync protocol for receiving file synchronization.
type Server struct {
	// TargetDir is the root directory where files will be synced to
	TargetDir string

	// BlockSize is the chunk size in bytes for delta synchronization
	BlockSize int
}

// NewServer creates a new Server instance with the given configuration.
func NewServer(targetDir string, blockSize int) *Server {
	return &Server{
		TargetDir: targetDir,
		BlockSize: blockSize,
	}
}

// HandleConnection processes a single client connection.
// This method performs the complete sync protocol:
// 1. Handshake validation
// 2. Receive source file list
// 3. Send destination file list with checksums
// 4. Receive and apply block descriptors
// 5. Send acknowledgment
//
// The connection is closed when this method returns.
// This method is safe to call concurrently from multiple goroutines.
func (s *Server) HandleConnection(conn net.Conn) error {
	defer conn.Close()

	// Read and validate handshake
	// conn.SetReadDeadline(time.Now().Add(handshakeReadDeadline))
	h, err := psync.ReadHandshake(conn)
	if err != nil {
		return fmt.Errorf("failed to read handshake: %w", err)
	}

	if !h.Valid() {
		return fmt.Errorf("invalid protocol header")
	}

	if h.Version != protoVersion {
		return fmt.Errorf("protocol version mismatch: got %d, expected %d", h.Version, protoVersion)
	}

	// Setup encoders/decoders
	br := bufio.NewReader(conn)
	dec := gob.NewDecoder(br)
	enc := gob.NewEncoder(conn)

	// Create session for this connection
	sess := &session{
		rcv: psync.Receiver{
			Root: s.TargetDir,
			Dec: decReader{
				Reader:  br,
				Decoder: dec,
			},
		},
		enc:       enc,
		dec:       dec,
		root:      s.TargetDir,
		blocksize: s.BlockSize,
	}

	// Run the sync loop
	return sess.syncLoop()
}

// session represents a single sync session for a connection.
type session struct {
	rcv       psync.Receiver
	enc       psync.Encoder
	dec       psync.Decoder
	root      string
	blocksize int
}

// syncLoop handles the main synchronization protocol loop.
// It can handle multiple sync operations over a single connection.
func (sess *session) syncLoop() error {
	for {
		// Receive source file list from client
		rs, delete, err := psync.RecvSrcFileList(sess.dec)
		if err != nil {
			return fmt.Errorf("recv src file list: %w", err)
		}

		// First remove extraneous files if requested
		if delete {
			if err := psync.DeleteExtra(rs, sess.root); err != nil {
				return fmt.Errorf("delete extra: %w", err)
			}
		}

		// Create empty directories
		// TODO: this feels a little tricky. so find a better
		// way to sync empty directories.
		if err := psync.MkDirs(rs, sess.root); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}

		// Send destination file list with checksums
		n, err := psync.SendDstFileList(sess.root, sess.blocksize, rs, sess.enc)
		if err != nil {
			return fmt.Errorf("send dst file list: %w", err)
		}

		if n == 0 {
			log.Println("nothing has been changed")
			continue
		}

		log.Printf("%d file(s) seems to have changed", n)

		// Receive block descriptors and build files
		err = sess.rcv.BuildFiles(n, rs)
		if err != nil {
			// Note: original code commented out error check, preserving behavior
			// return fmt.Errorf("build files: %w", err)
		}

		// Send acknowledgment
		if err := sess.enc.Encode(uint32(0x1a2b)); err != nil {
			return fmt.Errorf("send ack: %w", err)
		}
	}
}

// decReader combines an io.Reader with a Decoder for the Receiver.
type decReader struct {
	io.Reader
	psync.Decoder
}
