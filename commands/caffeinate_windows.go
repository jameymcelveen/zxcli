//go:build windows

package commands

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	setExecState   = kernel32.NewProc("SetThreadExecutionState")
	getConsoleMode = kernel32.NewProc("GetConsoleMode")
	setConsoleMode = kernel32.NewProc("SetConsoleMode")
	getScreenInfo  = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

// keepAwake holds the execution state on a dedicated OS thread, because
// Windows ties it to the thread that set it.
func keepAwake(why string, display bool) (release func(), how string, err error) {
	flags := uintptr(esContinuous | esSystemRequired)
	if display {
		flags |= esDisplayRequired
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer close(finished)
		setExecState.Call(flags)
		<-done
		setExecState.Call(esContinuous)
	}()
	return func() { close(done); <-finished }, "SetThreadExecutionState", nil
}

// rawMode makes the console hand over single keys as they are pressed.
func rawMode() (func(), error) {
	h := os.Stdin.Fd()
	var mode uint32
	if r, _, e := getConsoleMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return nil, e
	}
	const lineInput, echoInput = 0x0002, 0x0004
	if r, _, e := setConsoleMode.Call(h, uintptr(mode&^(lineInput|echoInput))); r == 0 {
		return nil, e
	}
	return func() { setConsoleMode.Call(h, uintptr(mode)) }, nil
}

func termSize() (w, h int) {
	var info struct {
		size, cursor [2]int16
		attrs        uint16
		window       [4]int16 // left, top, right, bottom
		maxSize      [2]int16
	}
	if r, _, _ := getScreenInfo.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&info))); r != 0 {
		w, h = int(info.window[2]-info.window[0])+1, int(info.window[3]-info.window[1])+1
		if w > 0 && h > 0 {
			return w, h
		}
	}
	return 80, 24
}
