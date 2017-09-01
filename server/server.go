package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"go/build"
	"io"
	"log"
	"net/http"

	"golang.org/x/net/websocket"

	"github.com/neelance/gopath-tunnel/protocol"
)

type Server struct {
	reqs  chan *reqResp
	cache protocol.Srcs
}

type reqResp struct {
	req  *protocol.Request
	resp interface{}
	done chan struct{}
}

func New() *Server {
	return &Server{
		reqs:  make(chan *reqResp, 100),
		cache: make(protocol.Srcs),
	}
}

func (s *Server) Handler() http.Handler {
	return websocket.Server{Handler: func(ws *websocket.Conn) {
		s.HandleStreams(ws, ws)
	}}
}

func (s *Server) HandleStreams(w io.Writer, r io.Reader) {
	dec := gob.NewDecoder(r)
	bw := bufio.NewWriter(w)
	enc := gob.NewEncoder(bw)

	if err := enc.Encode(&protocol.Request{Action: protocol.ActionVersion}); err != nil {
		log.Print(err)
		return
	}
	bw.Flush()

	var version int
	if err := dec.Decode(&version); err != nil {
		log.Print(err)
		return
	}

	if version != 1 {
		if err := enc.Encode(&protocol.Request{Action: protocol.ActionError, Error: "wrong version"}); err != nil {
			log.Print(err)
			return
		}
		bw.Flush()
		return
	}

	for rr := range s.reqs {
		if err := enc.Encode(rr.req); err != nil {
			log.Print(err)
			s.reqs <- rr
			return
		}
		bw.Flush()

		if err := dec.Decode(rr.resp); err != nil {
			if err != io.EOF {
				log.Print(err)
			}
			s.reqs <- rr
			return
		}
		close(rr.done)
	}
}

func (s *Server) List(ctx context.Context) ([]string, error) {
	var pkgs []string
	done := make(chan struct{})
	s.reqs <- &reqResp{
		req: &protocol.Request{
			Action: protocol.ActionList,
		},
		resp: &pkgs,
		done: done,
	}

	select {
	case <-done:
		// ok
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return pkgs, nil
}

func (s *Server) Fetch(ctx context.Context, importPath string, includeTests bool) (protocol.Srcs, error) {
	cached := make(map[protocol.SrcID][]byte)
	for id, src := range s.cache {
		cached[id] = src.Hash
	}

	var srcs protocol.Srcs
	done := make(chan struct{})
	s.reqs <- &reqResp{
		req: &protocol.Request{
			Action: protocol.ActionFetch,
			SrcID: protocol.SrcID{
				ImportPath:   importPath,
				IncludeTests: includeTests,
			},
			Cached:      cached,
			GOARCH:      build.Default.GOARCH,
			GOOS:        build.Default.GOOS,
			ReleaseTags: build.Default.ReleaseTags,
		},
		resp: &srcs,
		done: done,
	}

	select {
	case <-done:
		// ok
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	for id, src := range srcs {
		if src.Files == nil {
			cachedSrc, ok := s.cache[id]
			if !ok || !bytes.Equal(cachedSrc.Hash, src.Hash) {
				return nil, errors.New("cache error")
			}
			src.Files = cachedSrc.Files
			continue
		}
		s.cache[id] = src
	}

	return srcs, nil
}
