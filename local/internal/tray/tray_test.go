package tray

import (
	"testing"
)

func TestTrayCallbacksRegistration(t *testing.T) {
	offCalled := false
	onCalled := false
	openCalled := false
	quitCalled := false

	Init(
		"Test",
		"0.1.0",
		func() { offCalled = true },
		func() { onCalled = true },
		func() { openCalled = true },
		func() { quitCalled = true },
	)

	goOnTurnOff()
	if !offCalled {
		t.Error("goOnTurnOff did not invoke callback")
	}

	goOnTurnOn()
	if !onCalled {
		t.Error("goOnTurnOn did not invoke callback")
	}

	goOnOpenWebsite()
	if !openCalled {
		t.Error("goOnOpenWebsite did not invoke callback")
	}

	goOnQuit()
	if !quitCalled {
		t.Error("goOnQuit did not invoke callback")
	}
}

func TestSetStatusNoPanic(t *testing.T) {
	SetStatus(true)
	SetStatus(false)
}
