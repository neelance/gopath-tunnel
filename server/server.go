package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/gob"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"

	"golang.org/x/net/http2"
	"golang.org/x/net/websocket"
	"golang.org/x/tools/godoc/vfs"
	"golang.org/x/tools/godoc/vfs/mapfs"

	"github.com/donovanhide/eventsource"
	"github.com/neelance/gopath-tunnel/protocol"
)

type Server struct {
	cache map[protocol.FileID][]byte

	mu sync.Mutex
	cl *http.Client
}

func (s *Server) Client() *http.Client {
	s.mu.Lock()
	cl := s.cl
	s.mu.Unlock()
	return cl
}

func (s *Server) SetClient(cl *http.Client) {
	s.mu.Lock()
	s.cl = cl
	s.mu.Unlock()
}

func New() *Server {
	return &Server{
		cache: make(map[protocol.FileID][]byte),
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
		if err := post(s.Client(), "/version", nil, &version); err != nil {
			log.Print(err)
			return
		}
		if version != 4 {
			req := &protocol.ErrorRequest{
				Error: "Incompatible client version. Please upgrade gopath-tunnel: go get -u github.com/neelance/gopath-tunnel",
			}
			if err := post(s.Client(), "/error", req, nil); err != nil {
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
	if err := post(s.Client(), "/packages", nil, &pkgs); err != nil {
		return nil, err
	}
	return pkgs, nil
}

func (s *Server) Fetch(ctx context.Context, importPath string, includeTests bool) (vfs.FileSystem, error) {
	var cached []protocol.FileID
	for id := range s.cache {
		cached = append(cached, id)
	}

	req := &protocol.FetchRequest{
		SrcID: protocol.SrcID{
			ImportPath:   importPath,
			IncludeTests: includeTests,
		},
		Cached: cached,
	}
	var resp protocol.FetchResponse
	if err := post(s.Client(), "/fetch", req, &resp); err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	for id, contents := range resp.Contents {
		s.cache[id] = contents
	}

	files := make(map[string]string)
	for name, id := range resp.Files {
		files[name] = string(s.cache[id])
	}
	return mapfs.New(files), nil
}

func (s *Server) Watch(ctx context.Context, importPath string, includeTests bool) (<-chan struct{}, error) {
	reqData := &protocol.FetchRequest{
		SrcID: protocol.SrcID{
			ImportPath:   importPath,
			IncludeTests: includeTests,
		},
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(reqData); err != nil {
		panic(err)
	}

	req, err := http.NewRequest("POST", "/watch", &buf)
	if err != nil {
		panic(err)
	}

	resp, err := s.Client().Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	c := make(chan struct{})
	go func() {
		defer resp.Body.Close()
		dec := eventsource.NewDecoder(resp.Body)
		for {
			event, err := dec.Decode()
			if err != nil {
				if err == context.Canceled {
					break
				}
				log.Println(err)
				break
			}

			if event.Data() == "changed" {
				c <- struct{}{}
			}
		}
	}()

	return c, nil
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
