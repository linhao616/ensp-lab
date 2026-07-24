package topology

import "testing"

func TestValidateIPConfig(t *testing.T) {
	// 合法拓扑：不应报任何错误
	good := &Topology{
		ID: "good",
		Devices: map[string]*Device{
			"s1": {
				ID: "s1",
				Interfaces: map[string]*Interface{
					"Ethernet0": {Name: "Ethernet0", IPAddress: "10.0.10.100", SubnetMask: "255.255.255.0", Gateway: "10.0.10.1"},
				},
			},
		},
	}
	if errs := ValidateIPConfig(good); len(errs) != 0 {
		t.Fatalf("expected no errors for valid topology, got %v", errs)
	}

	// 非法拓扑：IP 末段>255、Gateway 含字母、SubnetMask 末段>255
	bad := &Topology{
		ID: "bad",
		Devices: map[string]*Device{
			"s3": {
				ID: "s3",
				Interfaces: map[string]*Interface{
					"Ethernet0": {Name: "Ethernet0", IPAddress: "10.0.10.300", SubnetMask: "255.255.255.0", Gateway: "10.0.10.1"},
				},
			},
			"s4": {
				ID: "s4",
				Interfaces: map[string]*Interface{
					"Ethernet0": {Name: "Ethernet0", IPAddress: "10.0.10.40", SubnetMask: "255.255.255.300", Gateway: "10.0.10.x"},
				},
			},
		},
	}
	errs := ValidateIPConfig(bad)
	if len(errs) < 3 {
		t.Fatalf("expected at least 3 errors (bad ip + bad mask + bad gw), got %d: %v", len(errs), errs)
	}

	// 空字段应视为合法：未配置 IP 的 L2 接口不应报错
	l2 := &Topology{
		ID: "l2",
		Devices: map[string]*Device{
			"sw": {
				ID:        "sw",
				Interfaces: map[string]*Interface{
					"10GE1/0/1": {Name: "10GE1/0/1", IPAddress: "", SubnetMask: "", Gateway: ""},
				},
			},
		},
	}
	if errs := ValidateIPConfig(l2); len(errs) != 0 {
		t.Fatalf("expected no errors for L2 interface with empty IP, got %v", errs)
	}
}
