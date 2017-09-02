package server

import (
	"bytes"
	"go/build"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/neelance/gopath-tunnel/protocol"
	"golang.org/x/tools/godoc/vfs"
	"golang.org/x/tools/godoc/vfs/mapfs"
)

func FileSystemForSources(srcs protocol.Srcs) vfs.FileSystem {
	fs := vfs.NewNameSpace()
	for id, src := range srcs {
		fs.Bind("/src/"+id.ImportPath, mapfs.New(src.Files), "/", vfs.BindReplace)
	}
	return fs
}

func SyncSourcesToDisk(srcs protocol.Srcs, srcDir string) error {
	for id, src := range srcs {
		dir := filepath.Join(srcDir, id.ImportPath)
		if err := os.MkdirAll(dir, 0777); err != nil && err != os.ErrExist {
			return err
		}

		fis, err := ioutil.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, fi := range fis {
			if fi.IsDir() {
				continue
			}
			if _, ok := src.Files[fi.Name()]; !ok {
				if err := os.Remove(filepath.Join(dir, fi.Name())); err != nil {
					return err
				}
			}
		}

		for basename, contents := range src.Files {
			filename := filepath.Join(dir, basename)
			byteContents := []byte(contents)

			if currentContents, err := ioutil.ReadFile(filename); err == nil {
				if bytes.Equal(currentContents, byteContents) {
					continue // don't modify file
				}
			}

			if err := ioutil.WriteFile(filename, byteContents, 0666); err != nil {
				return err
			}
		}
	}
	return nil
}

func BuildContextForFileSystem(fs vfs.FileSystem) *build.Context {
	return &build.Context{
		GOROOT:      "/",
		Compiler:    "gc",
		ReleaseTags: build.Default.ReleaseTags,
		JoinPath:    path.Join,
		SplitPathList: func(list string) []string {
			return []string{list}
		},
		IsAbsPath: path.IsAbs,
		IsDir: func(path string) bool {
			fi, err := fs.Stat(path)
			return err == nil && fi.IsDir()
		},
		HasSubdir: func(root, dir string) (rel string, ok bool) {
			panic("not implemented")
		},
		ReadDir: fs.ReadDir,
		OpenFile: func(path string) (io.ReadCloser, error) {
			return fs.Open(path)
		},
	}
}
