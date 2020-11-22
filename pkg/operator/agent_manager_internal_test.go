package operator

import (
	"testing"
)

func TestUpdateAgentLabelMissing(t *testing.T) {
	if _, err := updateAgentLabelMissing(); err != nil {
		t.Fatalf("Requirement must be valid, got: %v", err)
	}
}
