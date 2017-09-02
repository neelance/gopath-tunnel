package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"fmt"
	"go/build"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kisielk/gotool"
	"github.com/neelance/gopath-tunnel/protocol"
	"golang.org/x/net/websocket"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: gopath-tunnel [url]")
		os.Exit(1)
	}

	url := os.Args[1]
	for {
		err := connect(url)
		fmt.Printf("Error: %s\n\n", err)
		time.Sleep(2 * time.Second)
	}
}

func connect(url string) error {
	fmt.Printf("Connecting to %s...\n", url)

	ws, err := websocket.Dial(url, "", "http://localhost/")
	if err != nil {
		return err
	}
	defer ws.Close()

	fmt.Println("Connected.")

	dec := gob.NewDecoder(ws)
	bw := bufio.NewWriter(ws)
	enc := gob.NewEncoder(bw)

	for {
		var req protocol.Request
		if err := dec.Decode(&req); err != nil {
			return err
		}

		var resp interface{}
		switch req.Action {
		case protocol.ActionVersion:
			resp = 1

		case protocol.ActionError:
			fmt.Println(req.Error)
			os.Exit(1)

		case protocol.ActionList:
			resp = gotool.ImportPaths([]string{"all"})

		case protocol.ActionFetch:
			context := &build.Context{
				GOROOT:      build.Default.GOROOT,
				GOPATH:      build.Default.GOPATH,
				GOARCH:      req.GOARCH,
				GOOS:        req.GOOS,
				BuildTags:   req.BuildTags,
				ReleaseTags: req.ReleaseTags,
				Compiler:    "gc",
			}

			srcs := make(protocol.Srcs)
			scanPackage(context, req.SrcID, req.Cached, srcs)
			resp = srcs

		default:
			return fmt.Errorf("protocol error")
		}

		if err := enc.Encode(resp); err != nil {
			return err
		}
		bw.Flush()
	}
}

func scanPackage(context *build.Context, srcID protocol.SrcID, cached map[protocol.SrcID][]byte, srcs protocol.Srcs) {
	if srcID.ImportPath == "C" {
		return
	}
	if _, ok := srcs[srcID]; ok {
		return
	}

	pkg, err := context.Import(srcID.ImportPath, "", 0)
	if err != nil {
		log.Fatal(err)
	}

	if pkg.Goroot {
		return
	}

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

	src := &protocol.Src{
		Hash: calculateHash(files),
	}
	if !bytes.Equal(src.Hash, cached[srcID]) { // only add files if not in cache
		src.Files = files
	}
	srcs[srcID] = src

	for _, imp := range pkg.Imports {
		scanPackage(context, protocol.SrcID{ImportPath: imp, IncludeTests: false}, cached, srcs)
	}
	if srcID.IncludeTests {
		for _, imp := range pkg.TestImports {
			scanPackage(context, protocol.SrcID{ImportPath: imp, IncludeTests: false}, cached, srcs)
		}
		for _, imp := range pkg.XTestImports {
			if imp == srcID.ImportPath {
				continue
			}
			scanPackage(context, protocol.SrcID{ImportPath: imp, IncludeTests: false}, cached, srcs)
		}
	}
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
