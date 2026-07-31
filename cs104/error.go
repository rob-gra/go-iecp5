// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"errors"
)

// error defined
var (
	ErrUseClosedConnection = errors.New("use of closed connection")
	ErrNotActive           = errors.New("server is not active")
	// ErrAlreadyStarted is returned by Start on a station that is already
	// running. Reporting it beats silently doing nothing: a caller that
	// started the same client twice has a bug, and the second call
	// succeeding hides it.
	ErrAlreadyStarted = errors.New("already started")
)
