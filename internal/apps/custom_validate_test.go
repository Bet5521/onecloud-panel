package apps

import (
	"strings"
	"testing"

	"onecloud-panel/internal/store"
)

// 自定义应用输入校验回归：systemd 单元注入、shell 注入、路径穿越与任意文件写。
func TestValidateCustomConfigRejectsInjection(t *testing.T) {
	cases := []struct {
		name string
		app  *store.CustomApp
	}{
		{"名称含换行(单元注入)", &store.CustomApp{Type: "binary", Name: "x\nExecStartPre=/bin/sh -c evil",
			ConfigJSON: `{"exec_name":"app"}`}},
		{"名称含说明符", &store.CustomApp{Type: "binary", Name: "x%n", ConfigJSON: `{"exec_name":"app"}`}},
		{"exec_name 路径穿越", &store.CustomApp{Type: "binary", Name: "n",
			ConfigJSON: `{"exec_name":"../../../../etc/cron.d/x"}`}},
		{"exec_name 含分隔符", &store.CustomApp{Type: "binary", Name: "n",
			ConfigJSON: `{"exec_name":"sub/app"}`}},
		{"workdir 任意路径", &store.CustomApp{Type: "binary", Name: "n",
			ConfigJSON: `{"exec_name":"app","workdir":"/etc/cron.d"}`}},
		{"workdir 相对路径", &store.CustomApp{Type: "binary", Name: "n",
			ConfigJSON: `{"exec_name":"app","workdir":"opt/x"}`}},
		{"workdir 含穿越", &store.CustomApp{Type: "binary", Name: "n",
			ConfigJSON: `{"exec_name":"app","workdir":"/opt/onecloud-apps/../../../etc"}`}},
		{"args 注入换行", &store.CustomApp{Type: "binary", Name: "n",
			ConfigJSON: `{"exec_name":"app","args":"a\nExecStartPre=evil"}`}},
		{"run 注入引号/换行", &store.CustomApp{Type: "github", Name: "n",
			ConfigJSON: `{"repo":"o/r","run":"echo hi\"\nExecStartPre=evil"}`}},
		{"repo 形如命令行选项", &store.CustomApp{Type: "github", Name: "n",
			ConfigJSON: `{"repo":"--upload-pack=evil"}`}},
		{"user 非法", &store.CustomApp{Type: "binary", Name: "n",
			ConfigJSON: `{"exec_name":"app","user":"root\nExecStartPre=evil"}`}},
		{"docker image 非法", &store.CustomApp{Type: "docker", Name: "n",
			ConfigJSON: `{"image":"bad image; rm -rf /"}`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateCustomConfig(c.app); err == nil {
				t.Fatalf("期望被拒绝，实际通过: %+v", c.app)
			}
		})
	}
}

func TestValidateCustomConfigAcceptsNormal(t *testing.T) {
	ok := []*store.CustomApp{
		{Type: "binary", Name: "我的服务", ConfigJSON: `{"exec_name":"my-app"}`},
		{Type: "binary", Name: "svc", ConfigJSON: `{"exec_name":"app","workdir":"/opt/onecloud-apps/3"}`},
		{Type: "github", Name: "gh", ConfigJSON: `{"repo":"Bet5521/onecloud-panel","branch":"main","run":"docker compose up -d"}`},
		{Type: "docker", Name: "dk", ConfigJSON: `{"image":"vaultwarden/server:latest","ports":["8081:80"],"restart":"unless-stopped"}`},
	}
	for _, app := range ok {
		if err := ValidateCustomConfig(app); err != nil {
			t.Fatalf("期望通过，实际被拒: %v (%+v)", err, app)
		}
	}
}

func TestSafeWorkDirNormalization(t *testing.T) {
	got, err := safeWorkDir("/opt/onecloud-apps/3/")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if got != "/opt/onecloud-apps/3" {
		t.Fatalf("归一化结果异常: %q", got)
	}
	if _, err := safeWorkDir("/var/lib/../../etc"); err == nil {
		t.Fatal("穿越路径应被拒绝")
	}
	if strings.Contains(mustWorkDir(t, "/data/apps"), "..") {
		t.Fatal("结果不应含 ..")
	}
}

func mustWorkDir(t *testing.T, v string) string {
	t.Helper()
	got, err := safeWorkDir(v)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	return got
}
