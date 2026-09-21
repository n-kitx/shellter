package render

import "testing"

// The window itself needs a display, so these cover the parts that do not:
// the Go side of the bridge.

func TestRunRequiresAFrameFunction(t *testing.T) {
	// Checked before any window is created, so this is safe headless.
	if err := Run(Config{}); err == nil {
		t.Fatal("expected an error when Config.Frame is nil")
	}
	if config() != nil {
		t.Fatal("a failed Run must not leave a window registered")
	}
}
