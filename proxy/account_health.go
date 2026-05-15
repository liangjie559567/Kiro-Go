package proxy

import (
	"fmt"
	"kiro-go/config"
	"kiro-go/logger"
	"kiro-go/pool"
	"time"
)

type healthCheckBatchResult struct {
	Success  int
	Failed   int
	Disabled int
}

type healthCheckStatus struct {
	Running        bool  `json:"running"`
	LastStartedAt  int64 `json:"lastStartedAt"`
	LastFinishedAt int64 `json:"lastFinishedAt"`
	NextRunAt      int64 `json:"nextRunAt"`
	LastSuccess    int   `json:"lastSuccess"`
	LastFailed     int   `json:"lastFailed"`
	LastDisabled   int   `json:"lastDisabled"`
	LastSkipped    bool  `json:"lastSkipped"`
}

func selectHealthCheckAccounts(accounts []config.Account) []config.Account {
	selected := make([]config.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Enabled {
			selected = append(selected, account)
		}
	}
	return selected
}

func runHealthCheckBatch(
	accounts []config.Account,
	autoDisable bool,
	check func(account *config.Account) error,
	disable func(account *config.Account, reason string, now int64) error,
	now int64,
) healthCheckBatchResult {
	var result healthCheckBatchResult
	for i := range accounts {
		err := check(&accounts[i])
		if err == nil {
			result.Success++
			continue
		}

		result.Failed++
		if !autoDisable || !shouldDisableHealthCheckFailure(err) {
			continue
		}
		if disableErr := disable(&accounts[i], err.Error(), now); disableErr != nil {
			logger.Warnf("[HealthCheck] Failed to disable %s: %v", accounts[i].Email, disableErr)
			continue
		}
		result.Disabled++
	}
	return result
}

func shouldDisableHealthCheckFailure(err error) bool {
	switch classifyFailureReason(err) {
	case pool.FailureReasonAuthExpired, pool.FailureReasonSuspended:
		return true
	default:
		return false
	}
}

func computeNextHealthCheckRunAt(now time.Time, settings config.HealthCheckConfig) int64 {
	if !settings.Enabled {
		return 0
	}

	interval := settings.IntervalMinutes
	if interval == 0 {
		interval = config.HealthCheckDefaultIntervalMinutes
	}
	return now.Add(time.Duration(interval) * time.Minute).Unix()
}

func (h *Handler) tryBeginHealthCheck(startedAt int64) bool {
	h.healthCheckMu.Lock()
	defer h.healthCheckMu.Unlock()

	if h.healthCheckStatus.Running {
		h.healthCheckStatus.LastSkipped = true
		return false
	}

	h.healthCheckStatus.Running = true
	h.healthCheckStatus.LastStartedAt = startedAt
	h.healthCheckStatus.LastSkipped = false
	return true
}

func (h *Handler) finishHealthCheck(result healthCheckBatchResult, finishedAt, nextRunAt int64) {
	h.healthCheckMu.Lock()
	defer h.healthCheckMu.Unlock()

	h.healthCheckStatus.Running = false
	h.healthCheckStatus.LastFinishedAt = finishedAt
	h.healthCheckStatus.NextRunAt = nextRunAt
	h.healthCheckStatus.LastSuccess = result.Success
	h.healthCheckStatus.LastFailed = result.Failed
	h.healthCheckStatus.LastDisabled = result.Disabled
}

func (h *Handler) setNextHealthCheckRun(nextRunAt int64) {
	h.healthCheckMu.Lock()
	defer h.healthCheckMu.Unlock()

	h.healthCheckStatus.NextRunAt = nextRunAt
}

func (h *Handler) getHealthCheckStatus() healthCheckStatus {
	h.healthCheckMu.RLock()
	defer h.healthCheckMu.RUnlock()

	return h.healthCheckStatus
}

func disableUnhealthyAccount(account *config.Account, reason string, now int64) error {
	accounts := config.GetAccounts()
	for i := range accounts {
		if accounts[i].ID != account.ID {
			continue
		}

		accounts[i].Enabled = false
		accounts[i].BanStatus = "UNHEALTHY"
		accounts[i].BanReason = reason
		accounts[i].BanTime = now

		account.Enabled = false
		account.BanStatus = accounts[i].BanStatus
		account.BanReason = accounts[i].BanReason
		account.BanTime = accounts[i].BanTime

		return config.UpdateAccount(account.ID, accounts[i])
	}
	return fmt.Errorf("account %s not found", account.ID)
}
