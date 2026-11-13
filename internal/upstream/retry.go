package upstream

import (
	"context"
	"errors"
	"time"
)

// RetryPolicy 控制重试与退避。
type RetryPolicy struct {
	// MaxAttempts 是最大尝试次数（含首次），默认 3。
	MaxAttempts int
	// InitialBackoff 是首次退避时长，默认 200ms。
	InitialBackoff time.Duration
	// MaxBackoff 是退避上限，默认 5s。
	MaxBackoff time.Duration
	// BackoffFactor 是退避倍率，默认 2。
	BackoffFactor float64
	// Retryable 判断错误是否可重试。
	Retryable func(error) bool
}

// DefaultRetryPolicy 返回默认重试策略。
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 200 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		BackoffFactor:  2,
		Retryable:      DefaultRetryable,
	}
}

// DefaultRetryable 判断错误是否值得重试。
//
// 超时、网络失败、5xx 可重试；请求无效与被拒绝不重试。
func DefaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTimeout) || errors.Is(err, ErrNetwork) {
		return true
	}
	return false
}

// DoWithRetry 按策略执行请求，遇可重试错误时退避后重试。
//
// 返回最后一次的错误（若最终失败）。
func DoWithRetry(ctx context.Context, fn func(context.Context) (*Response, error), policy RetryPolicy) (*Response, error) {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 1
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = 200 * time.Millisecond
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = 5 * time.Second
	}
	if policy.BackoffFactor <= 0 {
		policy.BackoffFactor = 2
	}
	if policy.Retryable == nil {
		policy.Retryable = DefaultRetryable
	}

	backoff := policy.InitialBackoff
	var lastErr error

	for attempt := range policy.MaxAttempts {
		if attempt > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			backoff = nextBackoff(backoff, policy)
		}

		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if !policy.Retryable(err) {
			return nil, err
		}
	}

	return nil, lastErr
}

// nextBackoff 计算下一次退避时长。
func nextBackoff(current time.Duration, policy RetryPolicy) time.Duration {
	next := time.Duration(float64(current) * policy.BackoffFactor)
	if next > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return next
}
