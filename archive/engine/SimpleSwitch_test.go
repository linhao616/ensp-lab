//go:build ignore

package engine

import (
	"strings"
	"testing"
)

func TestSimpleSwitch_PingTest(t *testing.T) {
	s, err := NewSimpleSwitch()
	if err != nil {
		t.Fatalf("NewSimpleSwitch failed: %v", err)
	}
	defer s.Close()

	result, err := s.PingTest()
	if err != nil {
		t.Fatalf("PingTest failed: %v", err)
	}

	t.Logf("Ping result: %s", result)

	if !strings.Contains(result, "0% packet loss") {
		t.Errorf("Expected ping success (0% packet loss), got: %s", result)
	}
}