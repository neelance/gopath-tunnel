package client

import (
	"context"
	"crypto/md5"
	"encoding/gob"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/donovanhide/eventsource"
	"github.com/fsnotify/fsnotify"
	"github.com/neelance/gopath-tunnel/protocol"
	"golang.org/x/tools/go/packages"
)

func NewHandler(patterns []string, gotError *bool) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("/version", gobHandler(func(r *http.Request) (interface{}, error) {
		return 4, nil
	}))

	mux.Handle("/error", gobHandler(func(r *http.Request) (interface{}, error) {
		var req *protocol.ErrorRequest
		if err := gob.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}

		fmt.Fprintln(os.Stderr, req.Error)
		*gotError = true

		return true, nil
	}))

	mux.Handle("/packages", gobHandler(func(r *http.Request) (interface{}, error) {
		pkgs, err := packages.Load(&packages.Config{
			Mode: packages.LoadFiles,
		}, patterns...)
		if err != nil {
			return nil, err
		}

		paths := []string{}
		for _, pkg := range pkgs {
			paths = append(paths, pkg.PkgPath)
		}
		return paths, nil
	}))

	mux.Handle("/fetch", gobHandler(func(r *http.Request) (interface{}, error) {
		var req protocol.FetchRequest
		if err := gob.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}

		resp := &protocol.FetchResponse{
			Files:    make(map[string]protocol.FileID),
			Contents: make(map[protocol.FileID][]byte),
		}
		if err := scanPackage(req.SrcID, req.Cached, resp); err != nil {
			return &protocol.FetchResponse{Error: err.Error()}, nil
		}
		return resp, nil
	}))

	mux.HandleFunc("/watch", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.FetchRequest
		if err := gob.NewDecoder(r.Body).Decode(&req); err != nil {
			panic(err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		enc := eventsource.NewEncoder(w, false)

		for {
			if err := waitForChange(r.Context(), req.SrcID); err != nil {
				if err == context.Canceled {
					return
				}
				panic(err)
			}

			if err := enc.Encode(protocol.ChangedEvent{}); err != nil {
				panic(err)
			}
			w.(http.Flusher).Flush()
		}
	})

	return mux
}

func scanPackage(srcID protocol.SrcID, cached []protocol.FileID, resp *protocol.FetchResponse) error {
	cachedMap := make(map[protocol.FileID]struct{})
	for _, id := range cached {
		cachedMap[id] = struct{}{}
	}

	addFile := func(dst, src string) {
		if _, ok := resp.Files[dst]; ok {
			return
		}

		contents, err := ioutil.ReadFile(src)
		if err != nil {
			log.Fatal(err)
		}

		h := md5.New()
		h.Write(contents)
		id := protocol.FileID(h.Sum(nil))

		resp.Files[dst] = id
		if _, ok := cachedMap[id]; !ok {
			resp.Contents[id] = contents
		}
	}

	deps, err := collectDependencies(srcID)
	if err != nil {
		return err
	}
	for _, pkg := range deps {
		for _, src := range append(pkg.GoFiles, pkg.OtherFiles...) {
			addFile(pkg.PkgPath+"/"+filepath.Base(src), src)
		}
	}

	return nil
}

func waitForChange(ctx context.Context, srcID protocol.SrcID) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	deps, err := collectDependencies(srcID)
	if err != nil {
		return err
	}
	for _, pkg := range deps {
		if err := watcher.Add(filepath.Dir(pkg.GoFiles[0])); err != nil {
			panic(err)
		}
	}

	var debounceTimeout <-chan time.Time
	for {
		select {
		case e := <-watcher.Events:
			if e.Op != fsnotify.Chmod {
				debounceTimeout = time.After(100 * time.Millisecond)
			}
		case <-debounceTimeout:
			return nil
		case err := <-watcher.Errors:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func collectDependencies(srcID protocol.SrcID) ([]*packages.Package, error) {
	var depedencies []*packages.Package
	seen := make(map[*packages.Package]struct{})
	var visit func(*packages.Package)
	visit = func(pkg *packages.Package) {
		if _, ok := seen[pkg]; ok {
			return
		}
		seen[pkg] = struct{}{}

		if pkg.PkgPath == "unsafe" || strings.HasSuffix(pkg.PkgPath, ".test") {
			return
		}
		if strings.HasPrefix(pkg.GoFiles[0], filepath.Join(runtime.GOROOT(), "src")) {
			return
		}

		depedencies = append(depedencies, pkg)

		for _, imp := range pkg.Imports {
			visit(imp)
		}
	}

	pkgs, err := packages.Load(&packages.Config{
		Mode:  packages.LoadImports,
		Tests: srcID.IncludeTests,
	}, srcID.ImportPath)
	if err != nil {
		return nil, err
	}

	for _, pkg := range pkgs {
		visit(pkg)
	}

	return depedencies, nil
}

func gobHandler(fn func(r *http.Request) (interface{}, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := fn(r)
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err = gob.NewEncoder(w).Encode(result); err != nil {
			panic(err)
		}
	})
}
