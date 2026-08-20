//go:build !windows

package state

import (
	"io/fs"
	"path/filepath"
)

func walkDir(root string, fn func(string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		return fn(path)
	})
}
