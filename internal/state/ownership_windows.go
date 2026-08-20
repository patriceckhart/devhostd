//go:build windows

package state

func ChownInvokingUser(path string) error     { return nil }
func ChownTreeInvokingUser(root string) error { return nil }
