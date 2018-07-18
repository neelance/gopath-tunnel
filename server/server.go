package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/gob"
	"errors"
	"go/build"
	"log"
	"net"
	"net/http"
	"sync"

	"golang.org/x/net/http2"
	"golang.org/x/net/websocket"

	"github.com/neelance/gopath-tunnel/protocol"
)

type Server struct {
	cache protocol.Srcs

	mu sync.Mutex
	cl *http.Client
}

func (s *Server) client() *http.Client {
	s.mu.Lock()
	cl := s.cl
	s.mu.Unlock()
	return cl
}

func New() *Server {
	return &Server{
		cache: make(protocol.Srcs),
	}
}

func (s *Server) Handler() http.Handler {
	return websocket.Handler(func(ws *websocket.Conn) {
		dialed := false
		s.mu.Lock()
		s.cl = &http.Client{
			Transport: &withDummyScheme{&http2.Transport{
				DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
					if dialed {
						panic("already dialed")
					}
					dialed = true
					return ws, nil
				},
			}},
		}
		s.mu.Unlock()

		var version int
		if err := post(s.client(), "/version", nil, &version); err != nil {
			log.Print(err)
			return
		}
		if version != 4 {
			req := &protocol.ErrorRequest{
				Error: "Incompatible client version. Please upgrade gopath-tunnel: go get -u github.com/neelance/gopath-tunnel",
			}
			if err := post(s.client(), "/error", req, nil); err != nil {
				log.Print(err)
				return
			}
			ws.Close()
			return
		}

		<-ws.Request().Context().Done()
	})
}

type withDummyScheme struct {
	t http.RoundTripper
}

func (t *withDummyScheme) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "https"
	return t.t.RoundTrip(req)
}

func (s *Server) List(ctx context.Context) ([]string, error) {
	var pkgs []string
	if err := post(s.client(), "/packages", nil, &pkgs); err != nil {
		return nil, err
	}
	return pkgs, nil
}

func (s *Server) Fetch(ctx context.Context, importPath string, includeTests bool) (protocol.Srcs, error) {
	cached := make(map[protocol.SrcID][]byte)
	for id, src := range s.cache {
		cached[id] = src.Hash
	}

	req := &protocol.FetchRequest{
		SrcID: protocol.SrcID{
			ImportPath:   importPath,
			IncludeTests: includeTests,
		},
		Cached:      cached,
		GOARCH:      build.Default.GOARCH,
		GOOS:        build.Default.GOOS,
		ReleaseTags: build.Default.ReleaseTags,
	}
	var resp protocol.FetchResponse
	if err := post(s.client(), "/fetch", req, &resp); err != nil {
		return nil, err
	}

	for id, src := range resp.Srcs {
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

	return resp.Srcs, nil
}

func post(c *http.Client, url string, reqData, respData interface{}) error {
	var buf bytes.Buffer
	if reqData != nil {
		if err := gob.NewEncoder(&buf).Encode(reqData); err != nil {
			return err
		}
	}

	resp, err := c.Post(url, "application/json", &buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if respData != nil {
		if err := gob.NewDecoder(resp.Body).Decode(respData); err != nil {
			return err
		}
	}

	return nil
}
