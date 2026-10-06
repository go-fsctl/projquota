// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build !linux

package projquota

import (
	"errors"
	"testing"
)

func TestUnsupportedOffLinux(t *testing.T) {
	var errs []error
	_, err := Detect("/")
	errs = append(errs, err)
	_, err = DetectFile(nil)
	errs = append(errs, err)
	_, _, err = GetProject("/")
	errs = append(errs, err)
	_, _, err = GetProjectFile(nil)
	errs = append(errs, err)
	errs = append(errs, SetProject("/", 1, true), SetProjectFile(nil, 1, true),
		SetProjectTree("/", 1), SetLimits("/", 1, Limits{}), SetLimitsFile(nil, 1, Limits{}))
	_, err = Usage("/", 1)
	errs = append(errs, err)
	_, err = UsageFile(nil, 1)
	errs = append(errs, err)
	for i, err := range errs {
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("call %d: got %v, want ErrUnsupported", i, err)
		}
	}
}
