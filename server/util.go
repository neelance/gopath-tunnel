package server

import (
	"go/build"
	"io"
	"os"
	"path"

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

func BuildContextForFileSystem(fs vfs.FileSystem) *build.Context {
	return &build.Context{
		GOROOT:   "/",
		Compiler: "gc",
		JoinPath: path.Join,
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

func WriteFileSystemToDisk(dstDir string, fs vfs.FileSystem, srcDir string) error {
	fis, err := fs.ReadDir(srcDir)
	if err != nil {
		return err
	}

	if err := os.Mkdir(dstDir, 0777); err != nil {
		return err
	}

	for _, fi := range fis {
		dstName := path.Join(dstDir, fi.Name())
		srcName := path.Join(srcDir, fi.Name())

		if fi.IsDir() {
			if err := WriteFileSystemToDisk(dstName, fs, srcName); err != nil {
				return err
			}
			continue
		}

		srcF, err := fs.Open(srcName)
		if err != nil {
			return err
		}

		dstF, err := os.Create(dstName)
		if err != nil {
			return err
		}

		io.Copy(dstF, srcF)

		srcF.Close()
		dstF.Close()
	}

	return nil
}
