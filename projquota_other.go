// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build !linux

package projquota

import "os"

// Project quotas are a Linux kernel feature. Off Linux every operation
// returns ErrUnsupported; the ABI and the unit conversions in abi.go and
// quota.go stay available and tested on every platform.

// Detect is unsupported off Linux.
func Detect(path string) (Filesystem, error) { return 0, ErrUnsupported }

// DetectFile is unsupported off Linux.
func DetectFile(f *os.File) (Filesystem, error) { return 0, ErrUnsupported }

// GetProject is unsupported off Linux.
func GetProject(path string) (uint32, bool, error) { return 0, false, ErrUnsupported }

// GetProjectFile is unsupported off Linux.
func GetProjectFile(f *os.File) (uint32, bool, error) { return 0, false, ErrUnsupported }

// SetProject is unsupported off Linux.
func SetProject(path string, id uint32, inherit bool) error { return ErrUnsupported }

// SetProjectFile is unsupported off Linux.
func SetProjectFile(f *os.File, id uint32, inherit bool) error { return ErrUnsupported }

// SetProjectTree is unsupported off Linux.
func SetProjectTree(root string, id uint32) error { return ErrUnsupported }

// SetLimits is unsupported off Linux.
func SetLimits(path string, id uint32, l Limits) error { return ErrUnsupported }

// SetLimitsFile is unsupported off Linux.
func SetLimitsFile(f *os.File, id uint32, l Limits) error { return ErrUnsupported }

// Usage is unsupported off Linux.
func Usage(path string, id uint32) (Quota, error) { return Quota{}, ErrUnsupported }

// UsageFile is unsupported off Linux.
func UsageFile(f *os.File, id uint32) (Quota, error) { return Quota{}, ErrUnsupported }
