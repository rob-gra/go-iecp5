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
	// ErrBufferFulled is kept for source compatibility but is no longer
	// returned by Send: the outbound send queue now evicts its oldest
	// entry on overflow instead of rejecting the newest.
	ErrBufferFulled = errors.New("buffer is full")
	ErrNotActive    = errors.New("server is not active")
)
