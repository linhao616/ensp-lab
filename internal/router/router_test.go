//go:build linux

package router

import (
	"testing"
	"time"

	"github.com/stv0g/gont/v2/pkg"
	"github.com/stv0g/gont/v2/pkg/options"
)

func TestFRRRouter_OSPF(t *testing.T) {
	n, err := gont.NewNetwork("test-ospf")
	if err != nil {
		t.Skipf("create network: %v (requires CAP_NET_ADMIN)", err)
	}
	defer n.Close()

	r1, err := n.AddHost("r1")
	if err != nil {
		t.Fatalf("add r1: %v", err)
	}

	r2, err := n.AddHost("r2")
	if err != nil {
		t.Fatalf("add r2: %v", err)
	}

	_, err = n.AddLink(
		options.Interface("eth0", r1, options.AddressIPv4(192, 168, 1, 1, 24)),
		options.Interface("eth0", r2, options.AddressIPv4(192, 168, 1, 2, 24)),
	)
	if err != nil {
		t.Fatalf("add link: %v", err)
	}

	router1 := NewFRRRouter(r1, "r1")
	router2 := NewFRRRouter(r2, "r2")

	if err := router1.Start(); err != nil {
		t.Skipf("start router1: %v (requires FRR installed)", err)
	}
	defer router1.Stop()

	if err := router2.Start(); err != nil {
		t.Skipf("start router2: %v (requires FRR installed)", err)
	}
	defer router2.Stop()

	if err := router1.ApplyOSPFConfig("192.168.1.0/24", "0.0.0.0"); err != nil {
		t.Fatalf("apply OSPF config on r1: %v", err)
	}

	if err := router2.ApplyOSPFConfig("192.168.1.0/24", "0.0.0.0"); err != nil {
		t.Fatalf("apply OSPF config on r2: %v", err)
	}

	time.Sleep(10 * time.Second)

	result, err := r1.Ping(r2)
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}

	t.Logf("Ping result: %s", result.String())

	if result.Loss != 0 {
		t.Errorf("Expected 0% packet loss, got %d%%", result.Loss)
	}
}

func TestFRRRouter_Cleanup(t *testing.T) {
	n, err := gont.NewNetwork("test-cleanup")
	if err != nil {
		t.Skipf("create network: %v (requires CAP_NET_ADMIN)", err)
	}
	defer n.Close()

	r1, err := n.AddHost("r1")
	if err != nil {
		t.Fatalf("add r1: %v", err)
	}

	router1 := NewFRRRouter(r1, "r1")

	if err := router1.Start(); err != nil {
		t.Skipf("start router1: %v (requires FRR installed)", err)
	}

	if !router1.IsRunning() {
		t.Error("Router should be running after Start()")
	}

	if err := router1.Stop(); err != nil {
		t.Fatalf("stop router1: %v", err)
	}

	if router1.IsRunning() {
		t.Error("Router should not be running after Stop()")
	}
}
