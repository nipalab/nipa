//go:build windows

package fsperm

import "os"

func readUmask() os.FileMode { return 0 }
