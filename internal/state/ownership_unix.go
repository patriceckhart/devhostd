//go:build !windows

package state

import (
	"os"
	"strconv"
)

func ChownInvokingUser(path string) error {
	uid, e := strconv.Atoi(os.Getenv("SUDO_UID"))
	if e != nil {
		return nil
	}
	gid, e := strconv.Atoi(os.Getenv("SUDO_GID"))
	if e != nil {
		return nil
	}
	return os.Chown(path, uid, gid)
}
func ChownTreeInvokingUser(root string) error {
	return filepathWalk(root, func(path string) error { return ChownInvokingUser(path) })
}
func filepathWalk(root string, fn func(string) error) error { return walkDir(root, fn) }
