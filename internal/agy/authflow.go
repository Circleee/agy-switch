package agy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"agy-switch/internal/creds"
	"agy-switch/internal/store"
)

// LoginFlowStatus 表示官方原生登录引导的实时状态
type LoginFlowStatus struct {
	Running bool   `json:"running"`
	Stage   string `json:"stage"` // "idle" | "waiting_browser" | "captured" | "cancelled" | "error"
	Email   string `json:"email,omitempty"`
	Message string `json:"message"`
}

// AuthFlowRunner 编排零第三方流量的正版 agy 登录、自动监听入库与取消自动回滚
type AuthFlowRunner struct {
	mu       sync.Mutex
	cancelFn context.CancelFunc
	running  bool
}

func NewAuthFlowRunner() *AuthFlowRunner {
	return &AuthFlowRunner{}
}

func (r *AuthFlowRunner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func (r *AuthFlowRunner) Cancel() {
	r.mu.Lock()
	fn := r.cancelFn
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// StartOfficialLogin 启动安全暂存 -> 拉起正版 agy 浏览器登录 -> 自动捕获归档 / 取消回滚
func (r *AuthFlowRunner) StartOfficialLogin(
	parentCtx context.Context,
	st *store.Store,
	syncer *creds.Syncer,
	onStatus func(LoginFlowStatus),
) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return errors.New("已有正在进行的登录流程，请先完成或取消当前登录")
	}
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
	r.running = true
	r.cancelFn = cancel
	r.mu.Unlock()

	emit := func(st LoginFlowStatus) {
		if onStatus != nil {
			onStatus(st)
		}
	}

	state, err := st.LoadState()
	if err != nil {
		r.finish()
		return err
	}

	// 1. 先回存当前激活账号并保留原始快照副本，确保取消或异常时 100% 恢复原账号
	_, _ = syncer.SyncBackActive(st)
	rollbackSnap := syncer.SnapshotRaw()

	// 2. 清空当前 5 路登录槽位，使正版 agy 启动时自动拉起浏览器 OAuth
	if err := syncer.ClearActive(); err != nil {
		_ = syncer.RestoreRaw(rollbackSnap)
		r.finish()
		return fmt.Errorf("准备干净登录环境失败: %w", err)
	}

	// 3. 拉起可见终端执行正版 agy
	if err := LaunchTerminalWithAgy(state.Config, nil, true); err != nil {
		_ = syncer.RestoreRaw(rollbackSnap)
		r.finish()
		return fmt.Errorf("启动正版 agy 登录终端失败: %w", err)
	}

	emit(LoginFlowStatus{
		Running: true,
		Stage:   "waiting_browser",
		Message: "已拉起正版 agy 终端，请在弹出的浏览器中完成 Google 官方授权（取消将自动恢复原账号）...",
	})

	go func() {
		defer r.finish()
		ticker := time.NewTicker(1200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				_ = syncer.RestoreRaw(rollbackSnap)
				emit(LoginFlowStatus{
					Running: false,
					Stage:   "cancelled",
					Message: "登录已取消或超时，已自动为您恢复原激活账号。",
				})
				return

			case <-ticker.C:
				bundle, err := syncer.CaptureActive()
				if err != nil || bundle == nil || bundle.AgyToken.IDTokenEmail == "" {
					continue
				}
				// 稍等 500ms 确保正版 agy 所有文件落盘完毕后再次读取最新快照
				time.Sleep(500 * time.Millisecond)
				if fresh, err2 := syncer.CaptureActive(); err2 == nil && fresh != nil {
					bundle = fresh
				}

				// 补齐全部 5 路凭证强一致性并写入本地保险箱
				if err := syncer.ApplyBundle(bundle); err != nil {
					_ = syncer.RestoreRaw(rollbackSnap)
					emit(LoginFlowStatus{
						Running: false,
						Stage:   "error",
						Message: fmt.Sprintf("同步新账号凭证失败，已回滚原账号: %v", err),
					})
					return
				}

				acc, err := st.UpsertAccount(bundle.AgyToken.IDTokenEmail, "", bundle, nil, true)
				if err != nil {
					emit(LoginFlowStatus{
						Running: false,
						Stage:   "error",
						Message: fmt.Sprintf("保存新账号失败: %v", err),
					})
					return
				}

				// 后台静默拉取一次新账号初始配额
				if qCtx, qCancel := context.WithTimeout(context.Background(), 25*time.Second); true {
					if q, qErr := QueryLiveQuota(qCtx, state.Config); qErr == nil {
						_ = st.UpdateQuota(acc.ID, q)
					}
					qCancel()
				}

				emit(LoginFlowStatus{
					Running: false,
					Stage:   "captured",
					Email:   acc.Email,
					Message: fmt.Sprintf("已成功登录并激活账号: %s", acc.Email),
				})
				return
			}
		}
	}()

	return nil
}

func (r *AuthFlowRunner) finish() {
	r.mu.Lock()
	r.running = false
	if r.cancelFn != nil {
		r.cancelFn()
		r.cancelFn = nil
	}
	r.mu.Unlock()
}
