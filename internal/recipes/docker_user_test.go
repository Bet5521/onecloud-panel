package recipes

import "testing"

// docker.user 允许 UID / UID:GID / name / name:group 四种写法，
// 只拦掉空白与空段这类必然导致 docker create 失败的输入。
func TestValidateDockerUser(t *testing.T) {
	pass := []string{"", "0", "0:0", "1001:1001", "openlist", "openlist:openlist"}
	for _, u := range pass {
		if err := validateDockerUser("demo", u); err != nil {
			t.Fatalf("%q 应通过校验，实际报错: %v", u, err)
		}
	}
	reject := []string{"0:0:0", "0:", ":0", "0 0", "0\n", "\t0"}
	for _, u := range reject {
		if err := validateDockerUser("demo", u); err == nil {
			t.Fatalf("%q 应被拒绝", u)
		}
	}
}
