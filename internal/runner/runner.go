// Package runner 负责后台长任务的持久化队列、执行与重启对账。
package runner

import (
	"context"
	"io"
	"log"
	"sync"
	"time"

	"onecloud-panel/internal/store"
)

// Handler 任务执行器；输出写入 w 即实时落库（节流）。
type Handler func(ctx context.Context, w io.Writer, t *store.BackgroundTask) error

// Decision 重启对账决定。
type Decision struct {
	// Status 为 success/failure；Requeue=true 时忽略 Status 重新排队。
	Status  string
	Requeue bool
	Error   string
	Note    string
}

// Reconciler 面板重启后依据节点实际状态判定任务终态。
type Reconciler func(ctx context.Context, t *store.BackgroundTask) (Decision, error)

const (
	workers    = 3
	pollEvery  = 2 * time.Second
	maxOutput  = 1 << 20 // 输出上限 1MiB，超限保留尾部
	flushEvery = time.Second
)

// FinishHook 任务到达终态时的回调（正常执行与重启对账两条路径均会触发一次）。
type FinishHook func(t *store.BackgroundTask, status string)

// Runner 后台任务运行器。
type Runner struct {
	store       *store.Store
	workers     int
	mu          sync.RWMutex
	handlers    map[string]Handler
	reconcilers map[string]Reconciler
	finishHooks []FinishHook
	nodeLocks   map[int64]*sync.Mutex
	notify      chan struct{}
}

// New 创建运行器。
func New(s *store.Store) *Runner {
	return &Runner{
		store:       s,
		workers:     workers,
		handlers:    map[string]Handler{},
		reconcilers: map[string]Reconciler{},
		nodeLocks:   map[int64]*sync.Mutex{},
		notify:      make(chan struct{}, 1),
	}
}

// OnFinish 注册任务终态回调。
func (r *Runner) OnFinish(h FinishHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishHooks = append(r.finishHooks, h)
}

func (r *Runner) fireFinish(t *store.BackgroundTask, status string) {
	r.mu.RLock()
	hooks := make([]FinishHook, len(r.finishHooks))
	copy(hooks, r.finishHooks)
	r.mu.RUnlock()
	for _, h := range hooks {
		h(t, status)
	}
}

// nodeLock 返回节点级互斥锁：同节点的任务串行执行（避免 apt 锁竞争等），
// 无节点归属的任务使用 0 号锁。
func (r *Runner) nodeLock(t *store.BackgroundTask) func() {
	id := int64(0)
	if t.NodeID != nil {
		id = *t.NodeID
	}
	r.mu.Lock()
	l, ok := r.nodeLocks[id]
	if !ok {
		l = &sync.Mutex{}
		r.nodeLocks[id] = l
	}
	r.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Register 注册任务处理器。
func (r *Runner) Register(taskType string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[taskType] = h
}

// RegisterReconciler 注册重启对账器。
func (r *Runner) RegisterReconciler(taskType string, rec Reconciler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconcilers[taskType] = rec
}

// Enqueue 入队任务并唤醒 worker。
func (r *Runner) Enqueue(taskType string, nodeID *int64, appID, payload string, createdBy *int64) (int64, error) {
	id, err := r.store.CreateTask(&store.BackgroundTask{
		Type: taskType, NodeID: nodeID, AppID: appID,
		Payload: payload, CreatedBy: createdBy,
	})
	if err != nil {
		return 0, err
	}
	r.Notify()
	return id, nil
}

// Notify 唤醒 worker（非阻塞）。
func (r *Runner) Notify() {
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *Runner) handlerFor(tp string) Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.handlers[tp]
}

func (r *Runner) reconcilerFor(tp string) Reconciler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.reconcilers[tp]
}

// Start 执行启动对账并启动 worker，阻塞至 ctx 取消。
func (r *Runner) Start(ctx context.Context) {
	r.reconcile(ctx)

	var wg sync.WaitGroup
	for i := 0; i < r.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.worker(ctx)
		}()
	}
	wg.Wait()
}

// worker 领取-执行循环。
func (r *Runner) worker(ctx context.Context) {
	for {
		t, err := r.store.ClaimQueuedTask()
		if err == nil {
			r.execute(ctx, t)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-r.notify:
		case <-time.After(pollEvery):
		}
	}
}

func (r *Runner) execute(ctx context.Context, t *store.BackgroundTask) {
	// 同节点任务串行：领取任务后先获取节点锁
	unlock := r.nodeLock(t)
	defer unlock()

	h := r.handlerFor(t.Type)
	tw := newTaskWriter(r.store, t.ID)
	defer tw.Close()

	if h == nil {
		_ = tw.WriteAll("[错误] 未注册的任务类型: " + t.Type + "\n")
		_ = r.store.FinishTask(t.ID, store.TaskFailure, "未注册的任务类型")
		r.fireFinish(t, store.TaskFailure)
		return
	}
	_ = tw.WriteAll("任务开始 (#" + itoa(t.ID) + " " + t.Type + ")\n")
	if err := h(ctx, tw, t); err != nil {
		_ = tw.WriteAll("\n[失败] " + err.Error() + "\n")
		if ferr := r.store.FinishTask(t.ID, store.TaskFailure, err.Error()); ferr != nil {
			log.Printf("任务 %d 终态写入失败: %v", t.ID, ferr)
		}
		r.fireFinish(t, store.TaskFailure)
		return
	}
	_ = tw.WriteAll("\n[成功]\n")
	if err := r.store.FinishTask(t.ID, store.TaskSuccess, ""); err != nil {
		log.Printf("任务 %d 终态写入失败: %v", t.ID, err)
	}
	r.fireFinish(t, store.TaskSuccess)
}

// reconcile 处理上次异常退出遗留的 running 任务。
func (r *Runner) reconcile(ctx context.Context) {
	running, err := r.store.ListRunningTasks()
	if err != nil {
		log.Printf("对账: 查询运行中任务失败: %v", err)
		return
	}
	for _, t := range running {
		r.reconcileOne(ctx, t)
	}
}

func (r *Runner) reconcileOne(ctx context.Context, t store.BackgroundTask) {
	rec := r.reconcilerFor(t.Type)
	if rec == nil {
		_ = r.store.AppendTaskOutput(t.ID,
			"\n[对账] 面板重启导致任务中断，且该任务类型无自动对账器；请检查节点实际状态后重试\n")
		_ = r.store.FinishTask(t.ID, store.TaskFailure, "面板重启中断，无对账器")
		r.fireFinish(&t, store.TaskFailure)
		return
	}
	d, err := rec(ctx, &t)
	if err != nil {
		_ = r.store.FinishTask(t.ID, store.TaskFailure, "对账失败: "+err.Error())
		r.fireFinish(&t, store.TaskFailure)
		return
	}
	if d.Requeue {
		note := "\n[对账] 面板重启后重新排队"
		if d.Note != "" {
			note += ": " + d.Note
		}
		note += "\n"
		if err := r.store.RequeueTask(t.ID, note); err != nil {
			log.Printf("对账重排任务 %d 失败: %v", t.ID, err)
		}
		r.Notify()
		return
	}
	if d.Note != "" {
		_ = r.store.AppendTaskOutput(t.ID, "\n[对账] "+d.Note+"\n")
	}
	status := store.TaskFailure
	switch d.Status {
	case store.TaskSuccess:
		status = store.TaskSuccess
		_ = r.store.FinishTask(t.ID, store.TaskSuccess, d.Error)
	case store.TaskFailure:
		_ = r.store.FinishTask(t.ID, store.TaskFailure, d.Error)
	default:
		_ = r.store.FinishTask(t.ID, store.TaskFailure, "对账返回未知状态")
	}
	r.fireFinish(&t, status)
}
