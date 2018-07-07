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
	"strings"
	"time"

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
			resp = 3

		case protocol.ActionError:
			fmt.Println(req.Error)
			os.Exit(1)

		case protocol.ActionList:
			var packages []string
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

			resp = packages

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
			if err := scanPackage(context, req.SrcID, req.Cached, srcs); err != nil {
				resp = &protocol.FetchResponse{Error: err.Error()}
				break
			}
			resp = &protocol.FetchResponse{Srcs: srcs}

		default:
			return fmt.Errorf("protocol error")
		}

		if err := enc.Encode(resp); err != nil {
			return err
		}
		bw.Flush()
	}
}

func scanPackage(context *build.Context, srcID protocol.SrcID, cached map[protocol.SrcID][]byte, srcs protocol.Srcs) error {
	if _, ok := srcs[srcID]; ok {
		return nil
	}

	pkg, err := context.Import(srcID.ImportPath, "", 0)
	if err != nil {
		return err
	}

	if pkg.Goroot {
		return nil
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

		if err := scanPackage(context, protocol.SrcID{ImportPath: impPkg.ImportPath, IncludeTests: false}, cached, srcs); err != nil {
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
