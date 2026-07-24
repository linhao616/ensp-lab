//go:build !linux

package router

import (
	"testing"
)

func TestFRRRouter_OSPF(t *testing.T) {
	t.Skip("FRR router requires Linux")
}

func TestFRRRouter_Cleanup(t *testing.T) {
	t.Skip("FRR router requires Linux")
}
