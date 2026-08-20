//go:build windows

package main

func elevated() bool              { return true }
func elevate(args []string) error { return nil }
