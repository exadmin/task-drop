//go:build !windows || (!amd64 && !arm64)

package main

import (
	"fmt"
	"os"
)

func preparePlatform(Config) {}
func reportFatal(err error)  { fmt.Fprintln(os.Stderr, err) }
