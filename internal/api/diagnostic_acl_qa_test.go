package api

// diagnostic_acl_qa_test.go —— Round 2 独立回归（QA：严过关）
//
// 独立验证 P1 硬约束：诊断面板 blockedBy 字段存在且正确。
// blockedBy 由 aclBlockFromDecision(cli.Decision) 生成，其输入是 cli 评估器
// 的 Decision。Round 1 已修复评估器使「中转设备自带 ACL」也能产出正确的
// deny Decision（device=r1, direction=inbound, acl=2000, rule=10）。本测试
// 验证该 Decision 被正确映射为 {device, acl, rule, direction}，且 permit 时为 nil。

import (
	"testing"

	"ensp-lab/internal/cli"
)

func TestQA_R2_aclBlockFromDecision_TransitDeny(t *testing.T) {
	dec := cli.Decision{
		Action:    "deny",
		DeviceID:  "r1",
		ACLNum:    "2000",
		Rule:      &cli.ACLRule{ID: 10},
		Direction: cli.DirInbound,
	}
	got := aclBlockFromDecision(dec)
	if got == nil {
		t.Fatalf("expected blockedBy map for deny decision, got nil")
	}
	if got["device"] != "r1" {
		t.Errorf("blockedBy.device = %v, want r1", got["device"])
	}
	if got["acl"] != "2000" {
		t.Errorf("blockedBy.acl = %v, want 2000", got["acl"])
	}
	if got["rule"] != 10 {
		t.Errorf("blockedBy.rule = %v, want 10", got["rule"])
	}
	if got["direction"] != "inbound" {
		t.Errorf("blockedBy.direction = %v, want inbound", got["direction"])
	}
}

func TestQA_R2_aclBlockFromDecision_PermitNil(t *testing.T) {
	dec := cli.Decision{Action: "permit", DeviceID: "r1"}
	if got := aclBlockFromDecision(dec); got != nil {
		t.Errorf("expected nil blockedBy for permit, got %v", got)
	}
}

func TestQA_R2_aclBlockFromDecision_NoRule(t *testing.T) {
	dec := cli.Decision{Action: "deny", DeviceID: "r2", ACLNum: "3000", Direction: cli.DirOutbound}
	got := aclBlockFromDecision(dec)
	if got == nil {
		t.Fatalf("expected blockedBy map for deny without rule, got nil")
	}
	if got["rule"] != 0 {
		t.Errorf("blockedBy.rule = %v, want 0 (no rule)", got["rule"])
	}
	if got["direction"] != "outbound" {
		t.Errorf("blockedBy.direction = %v, want outbound", got["direction"])
	}
}
