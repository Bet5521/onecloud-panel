package apps

import (
	"encoding/json"
	"strings"
	"testing"

	"onecloud-panel/internal/recipes"
)

// docker.user 必须如实进入容器创建请求体。OpenList v4.1.0+ 等镜像已移除
// PUID/PGID，只认容器运行身份，写不进去就会以 openlist(1001) 启动并因宿主
// 绑定目录属主是 root 而反复重启。
func TestBuildCreateBodyIncludesUser(t *testing.T) {
	ds := &recipes.DockerSpec{Image: "demo:latest", User: "0:0"}
	body, err := buildCreateBody(ds.Image, ds, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("create body 不是合法 JSON: %v", err)
	}
	if got["User"] != "0:0" {
		t.Fatalf("User 未写入 create body: %s", body)
	}
}

// 未声明 user 时不应出现该字段，避免给所有容器都塞一个空 User。
func TestBuildCreateBodyOmitsEmptyUser(t *testing.T) {
	ds := &recipes.DockerSpec{Image: "demo:latest"}
	body, err := buildCreateBody(ds.Image, ds, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"User"`) {
		t.Fatalf("空 user 不应出现在 create body: %s", body)
	}
}
