//go:build !windows

package core

import "os"

func enableVT(*os.File) bool { return true }
