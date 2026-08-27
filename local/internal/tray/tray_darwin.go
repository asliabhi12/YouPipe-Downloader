//go:build darwin && cgo

package tray

/*
#cgo LDFLAGS: -framework Cocoa -framework AppKit
#include <stdlib.h>
#include "tray_darwin.h"
*/
import "C"
import (
	"os"
	"os/exec"
	"runtime"
	"unsafe"
)

var (
	onTurnOffFn     func()
	onTurnOnFn      func()
	onOpenWebsiteFn func()
	onQuitFn        func()
)

//export goOnTurnOff
func goOnTurnOff() {
	if onTurnOffFn != nil {
		onTurnOffFn()
	}
}

//export goOnTurnOn
func goOnTurnOn() {
	if onTurnOnFn != nil {
		onTurnOnFn()
	}
}

//export goOnOpenWebsite
func goOnOpenWebsite() {
	if onOpenWebsiteFn != nil {
		onOpenWebsiteFn()
	} else {
		OpenDefaultBrowser("https://youpiper.com")
	}
}

//export goOnQuit
func goOnQuit() {
	if onQuitFn != nil {
		onQuitFn()
	} else {
		os.Exit(0)
	}
}

func OpenDefaultBrowser(url string) {
	_ = exec.Command("open", url).Run()
}

func Init(title, version string, onTurnOff, onTurnOn, onOpenWebsite, onQuit func()) {
	onTurnOffFn = onTurnOff
	onTurnOnFn = onTurnOn
	onOpenWebsiteFn = onOpenWebsite
	onQuitFn = onQuit

	cTitle := C.CString(title)
	cVersion := C.CString(version)
	defer C.free(unsafe.Pointer(cTitle))
	defer C.free(unsafe.Pointer(cVersion))

	C.initTray(cTitle, cVersion)
}

func SetStatus(running bool) {
	r := C.int(0)
	if running {
		r = C.int(1)
	}
	C.setTrayStatus(r)
}

func Run() {
	runtime.LockOSThread()
	C.runTray()
}

func Stop() {
	C.stopTray()
}
