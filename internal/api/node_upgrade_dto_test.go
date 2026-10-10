package api

import (
	"testing"

	"onecloud-panel/internal/store"
)

// 节点 DTO 必须带上最近一次升级结果，界面才能展示失败原因 /
// 「已替换但未生效」；AppliedVersion 以当前上报的 agent_version 为准，
// 避免展示 Agent 自己那份可能已经过期的判断。
func TestNodeDTOExposesUpgradeResult(t *testing.T) {
	n := &store.Node{
		ID: 1, Mode: "local", Name: "n",
		AgentVersion: "2.2.7",
		UpgradeJSON:  `{"target_version":"2.2.8","applied_version":"2.2.8","ok":true,"at":1700000000}`,
	}
	d := toDTO(n)
	if d.Upgrade == nil {
		t.Fatal("应解析出 upgrade 字段")
	}
	if d.Upgrade.TargetVersion != "2.2.8" || !d.Upgrade.OK {
		t.Fatalf("升级结果解析有误: %+v", d.Upgrade)
	}
	if d.Upgrade.AppliedVersion != "2.2.7" {
		t.Fatalf("AppliedVersion 应取当前 agent_version，实际 %q", d.Upgrade.AppliedVersion)
	}
	if d.Upgrade.Effective() {
		t.Fatal("目标 2.2.8 而实际运行 2.2.7，应判定为「已替换但未生效」")
	}
}

// 没有升级记录、或记录是脏数据时，不应输出该字段也不应 panic。
func TestNodeDTOOmitsUpgradeWhenAbsentOrCorrupt(t *testing.T) {
	if d := toDTO(&store.Node{ID: 2, Mode: "local"}); d.Upgrade != nil {
		t.Fatalf("无升级记录时不应输出 upgrade: %+v", d.Upgrade)
	}
	if d := toDTO(&store.Node{ID: 3, Mode: "local", UpgradeJSON: "{not-json"}); d.Upgrade != nil {
		t.Fatalf("脏数据时不应输出 upgrade: %+v", d.Upgrade)
	}
}
