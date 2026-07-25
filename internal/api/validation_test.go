package api

import (
	"strings"
	"testing"

	"ensp-lab/internal/topology"
)

func TestValidateTopoID(t *testing.T) {
	long := strings.Repeat("a", 65)
	cases := []struct {
		id string
		ok bool
	}{
		{"", true},
		{"abc123", true},
		{"topo-1_X", true},
		{"a/b", false},
		{"..", false},
		{"a.b", false},
		{"has space", false},
		{long, false},
	}
	for _, c := range cases {
		err := validateTopoID(c.id)
		if c.ok && err != nil {
			t.Errorf("validateTopoID(%q) unexpected error: %v", c.id, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validateTopoID(%q) expected error, got nil", c.id)
		}
	}
}

func TestIsValidDeviceType(t *testing.T) {
	if !IsValidDeviceType(topology.DeviceRouter) {
		t.Error("DeviceRouter should be valid")
	}
	if IsValidDeviceType(topology.DeviceType("bogus")) {
		t.Error("bogus device type should be invalid")
	}
	if IsValidDeviceType("") {
		t.Error("empty device type should be invalid")
	}
}

func TestValidateIP(t *testing.T) {
	ok := []string{"", "192.168.1.1", "10.0.0.0", "::1", "fe80::1"}
	for _, v := range ok {
		if err := validateIP(v); err != nil {
			t.Errorf("validateIP(%q) unexpected error: %v", v, err)
		}
	}
	bad := []string{"999.1.1.1", "abc", "1.2.3", ":::"}
	for _, v := range bad {
		if err := validateIP(v); err == nil {
			t.Errorf("validateIP(%q) expected error, got nil", v)
		}
	}
}

func TestValidateCIDR(t *testing.T) {
	if err := validateCIDR("192.168.1.0/24"); err != nil {
		t.Errorf("valid CIDR rejected: %v", err)
	}
	for _, v := range []string{"", "192.168.1.0", "foo", "10.0.0.0/33"} {
		if err := validateCIDR(v); err == nil {
			t.Errorf("validateCIDR(%q) expected error, got nil", v)
		}
	}
}

func TestValidateOSPFArea(t *testing.T) {
	ok := []string{"0", "1", "65535", "4294967295", "0.0.0.0", "255.255.255.255"}
	for _, v := range ok {
		if err := validateOSPFArea(v); err != nil {
			t.Errorf("validateOSPFArea(%q) unexpected error: %v", v, err)
		}
	}
	bad := []string{"", "abc", "256.0.0.0", "1.2.3", "4294967296"}
	for _, v := range bad {
		if err := validateOSPFArea(v); err == nil {
			t.Errorf("validateOSPFArea(%q) expected error, got nil", v)
		}
	}
}

func TestValidateASN(t *testing.T) {
	if err := validateASN(0); err == nil {
		t.Error("ASN 0 should be rejected")
	}
	if err := validateASN(1); err != nil {
		t.Errorf("ASN 1 rejected: %v", err)
	}
	if err := validateASN(4294967295); err != nil {
		t.Errorf("ASN 4294967295 rejected: %v", err)
	}
}

func TestValidateTopologyPayload(t *testing.T) {
	// 合法拓扑：两个设备 + 一条互联链路。
	good := topology.NewTopology("t1", "lab")
	d1 := &topology.Device{ID: "d1", Name: "r1", Type: topology.DeviceRouter}
	d2 := &topology.Device{ID: "d2", Name: "s1", Type: topology.DeviceSwitch}
	good.AddDevice(d1)
	good.AddDevice(d2)
	good.AddLink(&topology.Link{
		ID: "l1", SourceDevice: "d1", TargetDevice: "d2",
		SourcePort: "GE0/0/0", TargetPort: "GE0/0/1",
	})
	if err := validateTopologyPayload(good); err != nil {
		t.Errorf("valid topology rejected: %v", err)
	}

	// 悬空链路：引用不存在的设备。
	dangling := topology.NewTopology("t2", "lab")
	dangling.AddDevice(&topology.Device{ID: "d1", Name: "r1", Type: topology.DeviceRouter})
	dangling.AddLink(&topology.Link{
		ID: "l1", SourceDevice: "d1", TargetDevice: "ghost",
		SourcePort: "GE0/0/0", TargetPort: "GE0/0/1",
	})
	if err := validateTopologyPayload(dangling); err == nil {
		t.Error("dangling link should be rejected")
	}

	// 非法设备类型。
	badType := topology.NewTopology("t3", "lab")
	badType.AddDevice(&topology.Device{ID: "d1", Name: "x", Type: topology.DeviceType("bogus")})
	if err := validateTopologyPayload(badType); err == nil {
		t.Error("invalid device type should be rejected")
	}

	// 名称含控制字符。
	badName := topology.NewTopology("t4", "lab\n")
	badName.AddDevice(&topology.Device{ID: "d1", Name: "x", Type: topology.DeviceRouter})
	if err := validateTopologyPayload(badName); err == nil {
		t.Error("control-char name should be rejected")
	}
}

func TestValidateIdent(t *testing.T) {
	if err := validateIdent("dev-1", maxIdentLen); err != nil {
		t.Errorf("valid ident rejected: %v", err)
	}
	if err := validateIdent("", maxIdentLen); err == nil {
		t.Error("empty ident should be rejected")
	}
	if err := validateIdent("a\nb", maxIdentLen); err == nil {
		t.Error("control-char ident should be rejected")
	}
}
