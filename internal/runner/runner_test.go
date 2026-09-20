package runner

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onecloud-panel/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TR-9.1 HTTP 断开不影响任务；流式输出与终态可查。
func TestRunnerExecuteStreaming(t *testing.T) {
	s := openStore(t)
	r := New(s)
	r.Register("slow", func(ctx context.Context, w io.Writer, tsk *store.BackgroundTask) error {
		for i := 1; i <= 10; i++ {
			fmt.Fprintf(w, "chunk-%d\n", i)
			time.Sleep(200 * time.Millisecond) // 共 2s，确保 1s 节流期间有部分输出落库
		}
		return nil
	})

	id, err := r.Enqueue("slow", nil, "", `{"x":1}`, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(ctx)

	// 等待期间模拟"HTTP 客户端早已断开"：此处完全不持有任务连接，
	// 只轮询数据库（输出在任务完成前就应已部分落库）
	sawPartial := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tsk, _ := s.GetTask(id)
		if strings.Contains(tsk.Output, "chunk-1") && tsk.Status == store.TaskRunning {
			sawPartial = true
		}
		if tsk.Status == store.TaskSuccess {
			if !sawPartial {
				t.Fatal("运行中未见流式输出提前落库")
			}
			for i := 1; i <= 10; i++ {
				want := fmt.Sprintf("chunk-%d", i)
				if !strings.Contains(tsk.Output, want) {
					t.Fatalf("输出缺少 %s: %q", want, tsk.Output)
				}
			}
			if tsk.StartedAt == nil || tsk.FinishedAt == nil {
				t.Fatal("时间戳缺失")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("任务未在时限内成功")
}

// 失败任务终态为 failure 且错误信息留存。
func TestRunnerFailure(t *testing.T) {
	s := openStore(t)
	r := New(s)
	r.Register("boom", func(ctx context.Context, w io.Writer, _ *store.BackgroundTask) error {
		fmt.Fprintln(w, "doing...")
		return fmt.Errorf("安装失败模拟")
	})
	id, _ := r.Enqueue("boom", nil, "", "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tsk, _ := s.GetTask(id)
		if tsk.Status == store.TaskFailure {
			if !strings.Contains(tsk.Error, "安装失败模拟") {
				t.Fatalf("error = %q", tsk.Error)
			}
			if !strings.Contains(tsk.Output, "doing...") {
				t.Fatalf("output = %q", tsk.Output)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("失败任务未终结")
}

// 未知任务类型直接标记失败。
func TestUnknownTaskType(t *testing.T) {
	s := openStore(t)
	r := New(s)
	id, _ := r.Enqueue("ghost", nil, "", "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tsk, _ := s.GetTask(id)
		if tsk.Status == store.TaskFailure {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("未知类型任务未失败")
}

// TR-9.2 面板重启：遗留 running 任务经对账器按节点实际状态判定成功。
func TestReconcileWithReconciler(t *testing.T) {
	s := openStore(t)

	// 模拟"上一次面板"留下的 running 任务
	id, _ := s.CreateTask(&store.BackgroundTask{Type: "install_app", AppID: "gitea"})
	if err := s.MarkTaskRunning(id); err != nil {
		t.Fatal(err)
	}

	r := New(s)
	r.RegisterReconciler("install_app", func(ctx context.Context, tsk *store.BackgroundTask) (Decision, error) {
		// 模拟到节点核查：systemd 单元已 active → 判定成功
		if tsk.AppID != "gitea" {
			t.Fatal("对账任务参数丢失")
		}
		return Decision{Status: store.TaskSuccess, Note: "节点服务状态 active，判定安装成功"}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r.Start(ctx) // Start 内先对账，无 queued 任务后空转

	tsk, err := s.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if tsk.Status != store.TaskSuccess {
		t.Fatalf("对账后状态 = %q, want success", tsk.Status)
	}
	if !strings.Contains(tsk.Output, "active") {
		t.Fatalf("对账说明未落库: %q", tsk.Output)
	}
}

// TR-9.2 无对账器的遗留任务标记失败，提示人工核查。
func TestReconcileWithoutReconciler(t *testing.T) {
	s := openStore(t)
	id, _ := s.CreateTask(&store.BackgroundTask{Type: "legacy_thing"})
	if err := s.MarkTaskRunning(id); err != nil {
		t.Fatal(err)
	}

	r := New(s)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r.Start(ctx)

	tsk, _ := s.GetTask(id)
	if tsk.Status != store.TaskFailure {
		t.Fatalf("状态 = %q, want failure", tsk.Status)
	}
	if !strings.Contains(tsk.Error, "重启") {
		t.Fatalf("error = %q", tsk.Error)
	}
}

// 对账器可决定重新排队，随后 worker 重新执行。
func TestReconcileRequeue(t *testing.T) {
	s := openStore(t)
	id, _ := s.CreateTask(&store.BackgroundTask{Type: "retryable"})
	_ = s.MarkTaskRunning(id)

	r := New(s)
	r.RegisterReconciler("retryable", func(ctx context.Context, _ *store.BackgroundTask) (Decision, error) {
		return Decision{Requeue: true, Note: "节点状态未知"}, nil
	})
	r.Register("retryable", func(ctx context.Context, w io.Writer, _ *store.BackgroundTask) error {
		fmt.Fprintln(w, "重跑完成")
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.Start(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tsk, _ := s.GetTask(id)
		if tsk.Status == store.TaskSuccess {
			if !strings.Contains(tsk.Output, "重跑完成") || !strings.Contains(tsk.Output, "重新排队") {
				t.Fatalf("output = %q", tsk.Output)
			}
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("重排任务未成功")
}
