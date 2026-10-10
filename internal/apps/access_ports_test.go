package apps

import (
	"testing"

	"onecloud-panel/internal/recipes"
)

// 访问入口：容器端口映射被覆盖后，状态应以实际宿主端口为准（而非配方默认）。
func TestAccessPortsFromInspectHostPorts(t *testing.T) {
	r := &recipes.Recipe{Ports: []recipes.PortSpec{
		{Port: 80, Proto: "tcp", Description: "Web 控制台"},
		{Port: 6800, Proto: "tcp", Description: "sync"},
	}}
	ci := containerInspect{
		HostConfig: containerHostConfig{NetworkMode: "bridge"},
		NetworkSettings: containerNetworkSettings{Ports: map[string][]portBinding{
			"80/tcp":   {{HostIP: "0.0.0.0", HostPort: "8090"}},
			"6800/tcp": {{HostIP: "0.0.0.0", HostPort: "6800"}},
		}},
	}
	got := accessPortsFromInspect(ci, r)
	if len(got) != 2 {
		t.Fatalf("端口数 = %d, want 2: %+v", len(got), got)
	}
	if got[0].Port != 6800 || got[1].Port != 8090 {
		t.Fatalf("应按宿主端口升序: %+v", got)
	}
	// 8090 由容器 80 映射而来，描述应沿用配方中 80 的说明。
	if got[1].Description != "Web 控制台" {
		t.Fatalf("描述应沿用配方: %+v", got[1])
	}
}

// host 网络模式无端口映射，应回退为容器端口本身。
func TestAccessPortsHostNetwork(t *testing.T) {
	r := &recipes.Recipe{Ports: []recipes.PortSpec{{Port: 6060, Proto: "tcp", Description: "WebUI"}}}
	ci := containerInspect{
		HostConfig:      containerHostConfig{NetworkMode: "host"},
		NetworkSettings: containerNetworkSettings{Ports: map[string][]portBinding{"6060/tcp": {}}},
	}
	got := accessPortsFromInspect(ci, r)
	if len(got) != 1 || got[0].Port != 6060 {
		t.Fatalf("host 模式应回退容器端口: %+v", got)
	}
}

// 配方回退：无 inspect 信息时以配方声明为准，协议空值按 tcp。
func TestRecipePortsFallback(t *testing.T) {
	got := recipePorts(&recipes.Recipe{Ports: []recipes.PortSpec{
		{Port: 1234, Proto: "", Description: "x"},
		{Port: 9, Proto: "UDP"},
		{Port: 0}, // 非法端口应忽略
	}})
	if len(got) != 2 || got[0].Port != 1234 || got[0].Proto != "tcp" || got[1].Proto != "udp" {
		t.Fatalf("recipePorts 结果错误: %+v", got)
	}
	if recipePorts(nil) == nil {
		t.Fatal("nil 配方应返回空列表而非 nil")
	}
}
