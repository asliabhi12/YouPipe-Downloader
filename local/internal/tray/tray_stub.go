//go:build !darwin || !cgo

package tray

func Init(title, version string, onTurnOff, onTurnOn, onOpenWebsite, onQuit func()) {}

func SetStatus(running bool) {}

func Run() {}

func Stop() {}

func OpenDefaultBrowser(url string) {}
