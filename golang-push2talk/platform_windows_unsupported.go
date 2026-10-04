//go:build windows && !amd64 && !arm64

package main

import "fmt"

func run(Config) error { return fmt.Errorf("Windows requires an amd64 or arm64 build") }
