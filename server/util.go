package server

import (
	"bytes"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"golang.org/x/tools/godoc/vfs"
)

func SyncFileSystemToDisk(fs vfs.FileSystem, rootDir string) error {
	// add new files
	if err := walk(fs, "/", func(fi os.FileInfo) error {
		fullName := filepath.Join(rootDir, fi.Name())
		if fi.IsDir() {
			return nil
		}

		if err := os.MkdirAll(path.Dir(fullName), 0777); err != nil {
			return err
		}

		newContents, err := readFile(fs, fi.Name())
		if err != nil {
			return err
		}

		if currentContents, err := ioutil.ReadFile(fullName); err == nil {
			if bytes.Equal(currentContents, newContents) {
				return nil // don't modify file
			}
		}

		if err := ioutil.WriteFile(fullName, newContents, 0666); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	// clean up removed files
	if err := walk(vfs.OS(rootDir), "/", func(fi os.FileInfo) error {
		fullName := filepath.Join(rootDir, fi.Name())
		if fi.IsDir() {
			if _, err := fs.ReadDir(fi.Name()); os.IsNotExist(err) {
				return os.Remove(fullName)
			}
			return nil
		}
		if _, err := fs.Lstat(fi.Name()); os.IsNotExist(err) {
			return os.Remove(fullName)
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func walk(fs vfs.FileSystem, dir string, visitor func(fi os.FileInfo) error) error {
	fis, err := fs.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, fi := range fis {
		fullName := path.Join(dir, fi.Name())
		if fi.IsDir() {
			if err := walk(fs, fullName, visitor); err != nil {
				return err
			}
		}
		if err := visitor(&walkFileInfo{fi, fullName}); err != nil {
			return err
		}
	}

	return nil
}

func readFile(fs vfs.FileSystem, name string) ([]byte, error) {
	f, err := fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return ioutil.ReadAll(f)
}

type walkFileInfo struct {
	os.FileInfo
	name string
}

func (fi *walkFileInfo) Name() string {
	return fi.name
}
