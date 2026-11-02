package protocol

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/treeverse/go-nfs/fsapi"
)

// Server serves NFSv3 and its mount protocol over a listener.
type Server struct {
	// Mounter hands out the filesystems this server exports.
	Mounter fsapi.Mounter
	// ID is the write verifier this server reports.  A client compares it
	// across requests to notice that the server restarted, and that the
	// writes it had not committed are gone.  Serve fills in a random one if
	// it is left zero.
	ID [8]byte
	context.Context

	// exports maps each filesystem Mounter has handed out to the id that
	// wire handles carry, so that a request arriving on any connection
	// resolves to the filesystem it is about.
	exports exports
}

// RegisterMessageHandler registers a handler for a specific
// XDR procedure.
func RegisterMessageHandler(protocol uint32, proc uint32, handler HandleFunc) error {
	if registeredHandlers == nil {
		registeredHandlers = make(map[registeredHandlerID]HandleFunc)
	}
	for k := range registeredHandlers {
		if k.protocol == protocol && k.proc == proc {
			return errors.New("already registered")
		}
	}
	id := registeredHandlerID{protocol, proc}
	registeredHandlers[id] = handler
	return nil
}

// HandleFunc serves one procedure of one RPC program.  Everything it needs
// beyond its own arguments - the filesystem a handle names, the mounter, the
// write verifier - it reaches through w.
type HandleFunc func(ctx context.Context, w *response) error

// TODO: store directly as a uint64 for more efficient lookups
type registeredHandlerID struct {
	protocol uint32
	proc     uint32
}

var registeredHandlers map[registeredHandlerID]HandleFunc

// Serve listens on the provided listener port for incoming client requests.  Once the
// server's Context is done each connection answers the request it is handling and closes.
// Serve returns nil once the caller has closed l and every connection has closed.
func (s *Server) Serve(l net.Listener) error {
	defer l.Close()
	baseCtx := context.Background()
	if s.Context != nil {
		baseCtx = s.Context
	}
	if bytes.Equal(s.ID[:], []byte{0, 0, 0, 0, 0, 0, 0, 0}) {
		if _, err := rand.Reader.Read(s.ID[:]); err != nil {
			return err
		}
	}

	// live holds every connection; a clean stop returns only once none is in flight.
	var live sync.WaitGroup
	var tempDelay time.Duration
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				live.Wait()
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		c := s.newConn(conn)
		live.Add(1)
		go func() {
			defer live.Done()
			c.serve(baseCtx)
		}()
	}
}

func (s *Server) newConn(nc net.Conn) *conn {
	c := &conn{
		Server: s,
		Conn:   nc,
	}
	return c
}

// TODO: keep an immutable map for each server instance to have less
// chance of races.
func (s *Server) handlerFor(prog uint32, proc uint32) HandleFunc {
	for k, v := range registeredHandlers {
		if k.protocol == prog && k.proc == proc {
			return v
		}
	}
	return nil
}

// Serve is a singleton listener paralleling http.Serve
func Serve(l net.Listener, mounter fsapi.Mounter) error {
	srv := &Server{Mounter: mounter}
	return srv.Serve(l)
}
