package account

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// Scheduled tasks of the account module (TASK-FRAMEWORK-DESIGN.md §14.1).
const (
	AutomaticRecoveryTask = "core.account.automatic_recovery"
	QuotaMirrorTask       = "core.account.quota_mirror"
	UsageLimitsTask       = "core.account.usage_limits"
	CredentialRefreshTask = "core.account.credential_refresh"
	QuotaSweepTask        = "core.account.quota_sweep"
	BalanceSweepTask      = "core.account.balance_sweep"
)

// RegisterTasks registers the cluster tasks of the account module and keeps
// tasks for the quota mirror, which registers its node task once warm (Run).
func (s *Service) RegisterTasks(tasks core.Tasks) error {
	s.tasks.Store(&tasks)
	specs := []core.TaskSpec{{
		Name:  AutomaticRecoveryTask,
		Title: core.LocalizedText{"en": "Release expired automatic disables", "zh": "自动禁用到点恢复"},
		Description: core.LocalizedText{
			"en": "Writes accounts whose timed automatic disable expired back to enabled, with their audit row and status event. Routing already serves them once the time passes; this is the bookkeeping. Runs once when a new node takes over.",
			"zh": "把定时自动禁用已到期的账号写回启用，并写审计和状态事件。到期后调度本来就会使用它们，这里只是补记录；新主节点接手时先跑一次。",
		},
		Scope:      core.TaskCluster,
		Every:      automaticStateSweepInterval,
		Timeout:    50 * time.Second,
		RunOnStart: true,
		Handler:    s.automaticRecoveryTask,
	}, {
		Name:  UsageLimitsTask,
		Title: core.LocalizedText{"en": "Apply account usage limits", "zh": "执行账号用量限制"},
		Description: core.LocalizedText{
			"en": "Folds the usage journal into hourly and lifetime sums, then disables accounts at their usage limit until the period ends and enables those back under it.",
			"zh": "把用量流水汇总到每小时和累计用量，再把达到用量限制的账号禁用到周期结束，把回到限制以下的账号重新启用。",
		},
		Scope:   core.TaskCluster,
		Every:   usageLimitsInterval,
		Timeout: time.Minute,
		Handler: s.usageLimitsTask,
	}, {
		Name:  CredentialRefreshTask,
		Title: core.LocalizedText{"en": "Renew expiring credentials", "zh": "续期快过期的凭证"},
		Description: core.LocalizedText{
			"en": "Renews the OAuth credentials of active accounts whose type declares refresh, some time before they expire; each renewal holds its account's lock. Runs once when a new node takes over.",
			"zh": "在过期前续期声明了续期能力的账号的 OAuth 凭证，每个账号续期时持有它自己的锁；新主节点接手时先跑一次。",
		},
		Scope:      core.TaskCluster,
		Every:      refreshSweepInterval,
		Timeout:    15 * time.Minute,
		RunOnStart: true,
		Handler:    s.credentialRefreshTask,
	}, {
		Name:  QuotaSweepTask,
		Title: core.LocalizedText{"en": "Query due account quotas", "zh": "查询到期的账号额度"},
		Description: core.LocalizedText{
			"en": "Creates the missing quota rows and queries the quotas that are due on accounts whose type can query them; every node that may work takes its share (rows are claimed in the database).",
			"zh": "为能查询额度的账号补建额度行，并查询到期的额度；每个可工作节点都跑，各自认领一部分（在数据库里认领）。",
		},
		Scope:       core.TaskNode,
		Every:       quotaFlushTick,
		Timeout:     5 * time.Minute,
		RequireWork: true,
		Handler:     s.quotaSweepTask,
	}, {
		Name:  BalanceSweepTask,
		Title: core.LocalizedText{"en": "Read due account balances", "zh": "查询到期的账号余额"},
		Description: core.LocalizedText{
			"en": "Reads the balances of accounts whose type declares one, each at its type's update interval: schedulable accounts, and accounts never read. A disabled account is read only when an administrator refreshes it. The balance is only shown.",
			"zh": "按账号类型声明的更新间隔查询账号余额：可调度的账号，以及从未查询过的账号。停用的账号只在管理员手动刷新时查询。余额只用于显示。",
		},
		Scope:   core.TaskCluster,
		Every:   time.Minute,
		Timeout: 15 * time.Minute,
		Handler: s.balanceSweepTask,
	}}
	for _, spec := range specs {
		if err := tasks.Register(spec); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) automaticRecoveryTask(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
	n, err := s.SweepAutomaticState(ctx)
	if err != nil {
		return core.TaskResult{}, err
	}
	if n > 0 {
		slog.InfoContext(ctx, "account: expired automatic disables written back", "accounts", n)
		return core.TaskResult{Count: n, Message: fmt.Sprintf("enabled %d accounts", n)}, nil
	}
	return core.TaskResult{}, nil
}

func (s *Service) usageLimitsTask(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
	return core.TaskResult{}, s.runUsageLimits(ctx)
}

func (s *Service) credentialRefreshTask(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
	n, err := s.SweepRefresh(ctx)
	if n > 0 {
		slog.InfoContext(ctx, "account: credentials renewed", "accounts", n)
	}
	return core.TaskResult{Count: n}, err
}

func (s *Service) quotaSweepTask(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
	n, err := s.sweepDueQuotas(ctx)
	return core.TaskResult{Count: n}, err
}

func (s *Service) balanceSweepTask(ctx context.Context, _ core.TaskRun) (core.TaskResult, error) {
	n, err := s.sweepDueBalances(ctx)
	return core.TaskResult{Count: n}, err
}

// runQuotaMirror warms this node's quota mirror (retrying with backoff), then
// registers the node task that reloads it every quotaMirrorReload; a
// reloadSoon then triggers that task.
func (s *Service) runQuotaMirror(ctx context.Context) {
	m := s.quotas()
	if !m.warm(ctx) || s.tasks.Load() == nil {
		return
	}
	tasks := *s.tasks.Load()
	err := tasks.Register(core.TaskSpec{
		Name:  QuotaMirrorTask,
		Title: core.LocalizedText{"en": "Reload this node's quota mirror", "zh": "刷新本节点额度镜像"},
		Description: core.LocalizedText{
			"en": "Reloads the quota snapshots this node keeps for routing by model availability; it also runs when plugins change the account types that declare it.",
			"zh": "重新加载本节点用于按模型可用性路由的额度快照；插件改变声明该能力的账号类型时也会立即运行。",
		},
		Scope:   core.TaskNode,
		Every:   quotaMirrorReload,
		Timeout: 30 * time.Second,
		Handler: m.reloadTask,
	})
	if err != nil {
		slog.ErrorContext(ctx, "account: register quota mirror task", "err", err)
		return
	}
	m.setTrigger(func() {
		tctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tasks.Trigger(tctx, QuotaMirrorTask); err != nil {
			slog.Warn("account: trigger quota mirror reload", "err", err)
		}
	})
}
