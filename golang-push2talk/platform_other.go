//go:build !windows

package main

import "fmt"

func run(Config) error {
	return fmt.Errorf("this platform needs native hotkey, microphone, and overlay implementations; Windows is supported")
}
