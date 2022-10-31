package internal

import (
	"encoding/json"
	"github.com/go-playground/validator/v10"
	"github.com/pkg/errors"
	"os"
	"time"
)

type Config struct {
	BundleBucket    string       `json:"bundleBucket" validate:"required"`
	ListenerRuleArn string       `json:"listenerRuleArn" validate:"required"`
	Target          *TargetSet   `json:"target" validate:"required"`
	RetryPolicy     *RetryPolicy `json:"retryPolicy" validate:"required"`
	TimeZone        *TimeZone    `json:"timeZone" validate:"required"`
}

type TargetSet struct {
	Blue  *Target `json:"blue" validate:"required"`
	Green *Target `json:"green" validate:"required"`
}

type Target struct {
	AutoScalingGroupName string `json:"autoScalingGroupName" validate:"required"`
	TargetGroupArn       string `json:"TargetGroupArn" validate:"required"`
}

type RetryPolicy struct {
	MaxLimit        int `json:"maxLimit"`
	IntervalSeconds int `json:"intervalSeconds"`
}

type TimeZone struct {
	Location string `json:"location"`
	Offset   int    `json:"offset"`
}

var location *time.Location

func (t *TimeZone) GetLocation() *time.Location {
	if location != nil {
		return location
	}
	location = time.FixedZone(t.Location, t.Offset)
	return location
}

func NewConfig(filename string) (*Config, error) {
	config := &Config{
		RetryPolicy: &RetryPolicy{
			MaxLimit:        360,
			IntervalSeconds: 10,
		},
		TimeZone: &TimeZone{
			Location: "Asia/Tokyo",
			Offset:   9 * 60 * 60,
		},
	}

	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, errors.Wrap(err, "ConfigReadError")
	}

	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, errors.Wrap(err, "ConfigParseError")
	}

	validate := validator.New()
	err = validate.Struct(config)
	if err != nil {
		return nil, errors.Wrap(err, "ConfigValidationError")
	}

	return config, nil
}
