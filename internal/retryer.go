package internal

import (
	"github.com/pkg/errors"
	"time"
)

var RetryTimeout = errors.New("RetryTimeout")

type RetryResult bool

const (
	FinishRetry   = true
	ContinueRetry = false
)

type Retryer interface {
	Start(hendler func(index int, interval *time.Duration) (RetryResult, error)) error
}

type FixedIntervalRetryer struct {
	maxLimit int
	interval time.Duration
}

func NewFixedIntervalRetryer(maxLimit int, interval time.Duration) *FixedIntervalRetryer {
	return &FixedIntervalRetryer{
		maxLimit: maxLimit,
		interval: interval,
	}
}

func (r FixedIntervalRetryer) Start(hendler func(index int, interval *time.Duration) (RetryResult, error)) error {
	for i := 0; i < r.maxLimit; i++ {
		result, err := hendler(i, &r.interval)
		if err != nil {
			return errors.Wrapf(err, "RetryFailure")
		}

		if result == FinishRetry {
			return nil
		}

		time.Sleep(r.interval)
	}

	return RetryTimeout
}
