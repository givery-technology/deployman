package assert

import (
	"fmt"
	"runtime"
	"testing"
)

func callerToString() string {
	caller := "missing"
	_, file, line, ok := runtime.Caller(2)
	if ok {
		caller = fmt.Sprintf("%s %d", file, line)
	}
	return caller
}

func True(t *testing.T, source bool) {
	if source == true {
		return
	}
	t.Errorf("got: false, want: true, caller: %s", callerToString())
}

func False(t *testing.T, source bool) {
	if source == false {
		return
	}
	t.Errorf("got: false, want: true, caller: %s", callerToString())
}

func Equal[T comparable](t *testing.T, source T, expect T) {
	if source == expect {
		return
	}
	t.Errorf("got: %+v, want: %+v, caller: %s", source, expect, callerToString())
}

func NotEqual[T comparable](t *testing.T, source T, expect T) {
	if source != expect {
		return
	}
	t.Errorf("got: %+v, want: %+v, caller: %s", source, expect, callerToString())
}

func Nil[T any](t *testing.T, source *T) {
	if source == nil {
		return
	}
	t.Errorf("got: not nil, want: nil, caller: %s", callerToString())
}

func NotNil[T any](t *testing.T, source *T) {
	if source != nil {
		return
	}
	t.Errorf("got: not nil, want: nil, caller: %s", callerToString())
}

func Success(t *testing.T, err error) {
	if err != nil {
		t.Error(err)
	}
}

func Failure(t *testing.T, err error) {
	if err == nil {
		t.Error(err)
	}
}
