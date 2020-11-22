package operator

import (
	"testing"
)

func TestWantsRebootSelector(t *testing.T) {
	if _, err := wantsRebootSelector(); err != nil {
		t.Fatalf("Selector must be valid, got: %v", err)
	}
}
