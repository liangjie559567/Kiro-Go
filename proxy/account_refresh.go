package proxy

import (
	"kiro-go/auth"
	"kiro-go/config"
	"kiro-go/logger"
	"kiro-go/pool"
	"strings"
	"time"
)

type refreshBatchResult struct {
	Success int
	Failed  int
}

type autoRefreshStatus struct {
	Running        bool  `json:"running"`
	LastStartedAt  int64 `json:"lastStartedAt"`
	LastFinishedAt int64 `json:"lastFinishedAt"`
	NextRunAt      int64 `json:"nextRunAt"`
	LastSuccess    int   `json:"lastSuccess"`
	LastFailed     int   `json:"lastFailed"`
	LastSkipped    bool  `json:"lastSkipped"`
}

type refreshAccountResult struct {
	Info    *config.AccountInfo
	Message string
}

func selectAutoRefreshAccounts(accounts []config.Account, scope string) []config.Account {
	if scope == config.AutoRefreshScopeAll {
		selected := make([]config.Account, len(accounts))
		copy(selected, accounts)
		return selected
	}

	selected := make([]config.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Enabled {
			selected = append(selected, account)
		}
	}
	return selected
}

func runRefreshBatch(accounts []config.Account, refresh func(account *config.Account) error) refreshBatchResult {
	var result refreshBatchResult
	for i := range accounts {
		if err := refresh(&accounts[i]); err != nil {
			result.Failed++
			continue
		}
		result.Success++
	}
	return result
}

func computeNextRunAt(now time.Time, settings config.AutoRefreshConfig) int64 {
	if !settings.Enabled {
		return 0
	}

	interval := settings.IntervalMinutes
	if interval == 0 {
		interval = config.AutoRefreshDefaultIntervalMinutes
	}
	return now.Add(time.Duration(interval) * time.Minute).Unix()
}

func (h *Handler) tryBeginAutoRefresh(startedAt int64) bool {
	h.autoRefreshMu.Lock()
	defer h.autoRefreshMu.Unlock()

	if h.autoRefreshStatus.Running {
		h.autoRefreshStatus.LastSkipped = true
		return false
	}

	h.autoRefreshStatus.Running = true
	h.autoRefreshStatus.LastStartedAt = startedAt
	h.autoRefreshStatus.LastSkipped = false
	return true
}

func (h *Handler) finishAutoRefresh(result refreshBatchResult, finishedAt, nextRunAt int64) {
	h.autoRefreshMu.Lock()
	defer h.autoRefreshMu.Unlock()

	h.autoRefreshStatus.Running = false
	h.autoRefreshStatus.LastFinishedAt = finishedAt
	h.autoRefreshStatus.NextRunAt = nextRunAt
	h.autoRefreshStatus.LastSuccess = result.Success
	h.autoRefreshStatus.LastFailed = result.Failed
}

func (h *Handler) setNextAutoRefreshRun(nextRunAt int64) {
	h.autoRefreshMu.Lock()
	defer h.autoRefreshMu.Unlock()

	h.autoRefreshStatus.NextRunAt = nextRunAt
}

func (h *Handler) getAutoRefreshStatus() autoRefreshStatus {
	h.autoRefreshMu.RLock()
	defer h.autoRefreshMu.RUnlock()

	return h.autoRefreshStatus
}

func refreshAccountData(account *config.Account) (*refreshAccountResult, error) {
	originalEnabled := account.Enabled

	refreshTokenIfNeeded := func() error {
		if account.RefreshToken == "" {
			return nil
		}

		newAccessToken, newRefreshToken, newExpiresAt, profileArn, err := auth.RefreshToken(account)
		if err != nil {
			return err
		}

		account.AccessToken = newAccessToken
		if newRefreshToken != "" {
			account.RefreshToken = newRefreshToken
		}
		account.ExpiresAt = newExpiresAt
		if err := config.UpdateAccountToken(account.ID, newAccessToken, newRefreshToken, newExpiresAt); err != nil {
			return err
		}
		pool.GetPool().UpdateToken(account.ID, newAccessToken, newRefreshToken, newExpiresAt)
		if profileArn != "" {
			account.ProfileArn = profileArn
			if err := config.UpdateAccountProfileArn(account.ID, profileArn); err != nil {
				return err
			}
		}
		return nil
	}

	if account.ExpiresAt > 0 && time.Now().Unix() > account.ExpiresAt-300 {
		if err := refreshTokenIfNeeded(); err != nil {
			return nil, err
		}
	}

	info, err := RefreshAccountInfo(account)
	if err != nil {
		if isSuspendedRefreshError(err) {
			return &refreshAccountResult{Message: "Account status updated"}, nil
		}

		if isAuthRefreshError(err) {
			if refreshErr := refreshTokenIfNeeded(); refreshErr == nil {
				info, err = RefreshAccountInfo(account)
				if err != nil && isSuspendedRefreshError(err) {
					return &refreshAccountResult{Message: "Account status updated"}, nil
				}
				if err == nil {
					if restoreErr := restoreAccountActiveAfterAuthRetry(account, originalEnabled); restoreErr != nil {
						return nil, restoreErr
					}
				}
			}
		}

		if err != nil {
			return nil, err
		}
	}

	if err := config.UpdateAccountInfo(account.ID, *info); err != nil {
		return nil, err
	}
	logger.Infof("[AutoRefresh] Refreshed %s: %s %.1f/%.1f", account.Email, info.SubscriptionType, info.UsageCurrent, info.UsageLimit)
	return &refreshAccountResult{Info: info}, nil
}

func restoreAccountActiveAfterAuthRetry(account *config.Account, originalEnabled bool) error {
	accounts := config.GetAccounts()
	for i := range accounts {
		if accounts[i].ID != account.ID {
			continue
		}

		accounts[i].Enabled = originalEnabled
		accounts[i].BanStatus = "ACTIVE"
		accounts[i].BanReason = ""
		accounts[i].BanTime = 0

		account.Enabled = accounts[i].Enabled
		account.BanStatus = accounts[i].BanStatus
		account.BanReason = accounts[i].BanReason
		account.BanTime = accounts[i].BanTime

		return config.UpdateAccount(account.ID, accounts[i])
	}

	account.Enabled = originalEnabled
	account.BanStatus = "ACTIVE"
	account.BanReason = ""
	account.BanTime = 0
	return nil
}

func isSuspendedRefreshError(err error) bool {
	errMsg := err.Error()
	return strings.Contains(errMsg, "TEMPORARILY_SUSPENDED") || strings.Contains(errMsg, "Account suspended")
}

func isAuthRefreshError(err error) bool {
	errMsg := err.Error()
	return strings.Contains(errMsg, "403") ||
		strings.Contains(errMsg, "401") ||
		strings.Contains(errMsg, "invalid") ||
		strings.Contains(errMsg, "expired")
}
