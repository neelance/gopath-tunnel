package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/gob"
	"fmt"
	"go/build"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/donovanhide/eventsource"
	"github.com/fsnotify/fsnotify"
	"github.com/neelance/gopath-tunnel/protocol"
	"golang.org/x/net/http2"
	"golang.org/x/net/websocket"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: gopath-tunnel [url]")
		os.Exit(1)
	}
	url := os.Args[1]

	gotError := false
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
		gotError = true

		return true, nil
	}))

	mux.Handle("/packages", gobHandler(func(r *http.Request) (interface{}, error) {
		packages := []string{}
		var scanDir func(root string, dir string)
		scanDir = func(root string, dir string) {
			fis, err := ioutil.ReadDir(filepath.Join(root, dir))
			if err != nil {
				return
			}

			hasGo := false
			for _, fi := range fis {
				name := fi.Name()
				if !fi.IsDir() {
					if strings.HasSuffix(name, ".go") {
						hasGo = true
					}
					continue
				}
				if name[0] == '.' ||
					name[0] == '_' ||
					name == "testdata" ||
					name == "node_modules" ||
					(dir == "" && (name == "builtin" || name == "mod")) {
					continue
				}
				scanDir(root, filepath.Join(dir, name))
			}

			if hasGo && dir != "" {
				packages = append(packages, dir)
			}
		}

		scanRoot := func(dir string) {
			scanDir(filepath.Join(dir, "src"), "")
		}
		scanRoot(build.Default.GOROOT)
		for _, gopath := range filepath.SplitList(build.Default.GOPATH) {
			scanRoot(gopath)
		}

		return packages, nil
	}))

	mux.Handle("/fetch", gobHandler(func(r *http.Request) (interface{}, error) {
		var req protocol.FetchRequest
		if err := gob.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}

		buildContext := &build.Context{
			GOROOT:      build.Default.GOROOT,
			GOPATH:      build.Default.GOPATH,
			GOARCH:      req.GOARCH,
			GOOS:        req.GOOS,
			BuildTags:   req.BuildTags,
			ReleaseTags: req.ReleaseTags,
			Compiler:    "gc",
		}

		srcs := make(protocol.Srcs)
		if err := scanPackage(buildContext, req.SrcID, req.Cached, srcs); err != nil {
			return &protocol.FetchResponse{Error: err.Error()}, nil
		}
		return &protocol.FetchResponse{Srcs: srcs}, nil
	}))

	mux.HandleFunc("/watch", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.FetchRequest
		if err := gob.NewDecoder(r.Body).Decode(&req); err != nil {
			panic(err)
		}

		buildContext := &build.Context{
			GOROOT:      build.Default.GOROOT,
			GOPATH:      build.Default.GOPATH,
			GOARCH:      req.GOARCH,
			GOOS:        req.GOOS,
			BuildTags:   req.BuildTags,
			ReleaseTags: req.ReleaseTags,
			Compiler:    "gc",
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		enc := eventsource.NewEncoder(w, false)

		for {
			if err := waitForChange(r.Context(), buildContext, req.SrcID); err != nil {
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

	for {
		fmt.Printf("Connecting to %s...\n", url)

		ws, err := websocket.Dial(url, "", "http://localhost/")
		if err != nil {
			fmt.Printf("Error: %s\n\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		fmt.Println("Connected.")

		s := &http2.Server{}
		s.ServeConn(ws, &http2.ServeConnOpts{Handler: mux})
		if gotError {
			os.Exit(1)
		}
		fmt.Printf("Connection lost.\n\n")

		time.Sleep(2 * time.Second)
	}
}

func scanPackage(context *build.Context, srcID protocol.SrcID, cached map[protocol.SrcID][]byte, srcs protocol.Srcs) error {
	dependencies := make(map[protocol.SrcID]*build.Package)
	if err := collectDependencies(context, srcID, dependencies); err != nil {
		return err
	}

	for _, pkg := range dependencies {
		files := make(map[string]string)
		addFiles := func(names []string) {
			for _, name := range names {
				contents, err := ioutil.ReadFile(filepath.Join(pkg.Dir, name))
				if err != nil {
					log.Fatal(err)
				}
				files[filepath.ToSlash(name)] = string(contents)
			}
		}
		addFiles(pkg.GoFiles)
		addFiles(pkg.CgoFiles)
		addFiles(pkg.CFiles)
		addFiles(pkg.CXXFiles)
		addFiles(pkg.MFiles)
		addFiles(pkg.HFiles)
		addFiles(pkg.FFiles)
		addFiles(pkg.SFiles)
		addFiles(pkg.SwigFiles)
		addFiles(pkg.SwigCXXFiles)
		addFiles(pkg.SysoFiles)
		if srcID.IncludeTests {
			addFiles(pkg.TestGoFiles)
			addFiles(pkg.XTestGoFiles)
		}
		filepath.Walk(filepath.Join(pkg.Dir, "testdata"), func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				addFiles([]string{path[len(pkg.Dir)+1:]})
			}
			return nil
		})

		src := &protocol.Src{
			Hash: calculateHash(files),
		}
		if !bytes.Equal(src.Hash, cached[srcID]) { // only add files if not in cache
			src.Files = files
			fmt.Printf("Uploading: %s\n", srcID.ImportPath)
		}
		srcs[srcID] = src
	}

	return nil
}

func waitForChange(ctx context.Context, buildContext *build.Context, srcID protocol.SrcID) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	dependencies := make(map[protocol.SrcID]*build.Package)
	if err := collectDependencies(buildContext, protocol.SrcID{ImportPath: "github.com/gorilla/mux"}, dependencies); err != nil {
		return err
	}

	for _, pkg := range dependencies {
		if err := watcher.Add(pkg.Dir); err != nil {
			return err
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

func collectDependencies(context *build.Context, srcID protocol.SrcID, dependencies map[protocol.SrcID]*build.Package) error {
	if _, ok := dependencies[srcID]; ok {
		return nil
	}

	pkg, err := context.Import(srcID.ImportPath, "", 0)
	if err != nil {
		return err
	}

	if pkg.Goroot {
		return nil
	}

	dependencies[srcID] = pkg

	imports := pkg.Imports
	if srcID.IncludeTests {
		imports = append(imports, pkg.TestImports...)
		imports = append(imports, pkg.XTestImports...)
	}
	for _, imp := range imports {
		if imp == "C" || imp == srcID.ImportPath {
			continue
		}

		impPkg, err := context.Import(imp, pkg.Dir, build.FindOnly)
		if err != nil {
			return err
		}

		if err := collectDependencies(context, protocol.SrcID{ImportPath: impPkg.ImportPath, IncludeTests: false}, dependencies); err != nil {
			return err
		}
	}

	return nil
}

func calculateHash(files map[string]string) []byte {
	h := md5.New()
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte(files[name]))
	}
	return h.Sum(nil)
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
