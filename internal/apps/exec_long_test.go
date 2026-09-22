package apps

import (
	"context"
	"io"
	"strings"
	"testing"

	"onecloud-panel/internal/executor"
	"onecloud-panel/internal/recipes"
)

// longFakeExec 记录哪些命令走了长任务通道（ExecLong），用于锁住
// 「apt/dpkg/systemctl 步骤必须走长超时」这一行为。
type longFakeExec struct {
	long  []string
	plain []string
}

func (f *longFakeExec) Exec(_ context.Context, name string, args ...string) (*executor.Result, error) {
	f.plain = append(f.plain, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return &executor.Result{ExitCode: 0}, nil
}

func (f *longFakeExec) ExecLong(_ context.Context, name string, args ...string) (*executor.Result, error) {
	f.long = append(f.long, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return &executor.Result{ExitCode: 0}, nil
}

func (f *longFakeExec) ExecStream(context.Context, io.Writer, string, ...string) (int, error) {
	return 0, nil
}
func (f *longFakeExec) ReadFile(string) ([]byte, error) { return nil, nil }
func (f *longFakeExec) WriteFile(string, []byte) error  { return nil }
func (f *longFakeExec) Exists(string) (bool, error)     { return false, nil }

// shortFakeExec 不实现 LongRunner，用于验证回落路径。
type shortFakeExec struct{ plain []string }

func (f *shortFakeExec) Exec(_ context.Context, name string, args ...string) (*executor.Result, error) {
	f.plain = append(f.plain, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return &executor.Result{ExitCode: 0}, nil
}
func (f *shortFakeExec) ExecStream(context.Context, io.Writer, string, ...string) (int, error) {
	return 0, nil
}
func (f *shortFakeExec) ReadFile(string) ([]byte, error) { return nil, nil }
func (f *shortFakeExec) WriteFile(string, []byte) error  { return nil }
func (f *shortFakeExec) Exists(string) (bool, error)     { return false, nil }

// 编译期断言：两个 fake 都满足 Executor；longFakeExec 额外满足 LongRunner。
var (
	_ executor.Executor   = (*longFakeExec)(nil)
	_ executor.LongRunner = (*longFakeExec)(nil)
	_ executor.Executor   = (*shortFakeExec)(nil)
)

func hasCmd(list []string, want string) bool {
	for _, c := range list {
		if strings.Contains(c, want) {
			return true
		}
	}
	return false
}

// apt 步骤在低配 ARM 节点上远超控制面 30s 超时，必须走 ExecLong。
func TestExecOneStepAptUsesLongRunner(t *testing.T) {
	fe := &longFakeExec{}
	if err := execOneStep(context.Background(), io.Discard, fe,
		recipes.Step{Apt: []string{"transmission-daemon"}}, ""); err != nil {
		t.Fatalf("apt 步骤失败: %v", err)
	}
	if !hasCmd(fe.long, "apt-get install -y --no-install-recommends transmission-daemon") {
		t.Fatalf("apt 安装未走长任务通道，long=%v plain=%v", fe.long, fe.plain)
	}
	if hasCmd(fe.plain, "apt-get install") {
		t.Fatalf("apt 安装不应走普通通道: %v", fe.plain)
	}
}

// exec / systemctl 步骤同样属于耗时操作，需走长任务通道。
func TestExecOneStepExecAndSystemctlUseLongRunner(t *testing.T) {
	fe := &longFakeExec{}
	if err := execOneStep(context.Background(), io.Discard, fe,
		recipes.Step{Exec: &recipes.ExecStep{Command: "systemctl", Args: []string{"restart", "x"}}}, ""); err != nil {
		t.Fatalf("exec 步骤失败: %v", err)
	}
	if err := execOneStep(context.Background(), io.Discard, fe,
		recipes.Step{Systemctl: &recipes.Systemctl{Action: "start", Unit: "x.service"}}, ""); err != nil {
		t.Fatalf("systemctl 步骤失败: %v", err)
	}
	if !hasCmd(fe.long, "systemctl restart x") || !hasCmd(fe.long, "systemctl start x.service") {
		t.Fatalf("未走长任务通道: long=%v", fe.long)
	}
}

// 未实现 LongRunner 的执行器回落为普通 Exec，行为与改动前一致。
func TestExecOneStepFallsBackWithoutLongRunner(t *testing.T) {
	fe := &shortFakeExec{}
	if err := execOneStep(context.Background(), io.Discard, fe,
		recipes.Step{Apt: []string{"vim"}}, ""); err != nil {
		t.Fatalf("apt 步骤失败: %v", err)
	}
	if !hasCmd(fe.plain, "apt-get install -y --no-install-recommends vim") {
		t.Fatalf("回落路径未执行安装: %v", fe.plain)
	}
}
