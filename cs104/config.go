// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"errors"
	"time"
)

const (
	// Port is the IANA registered port number for unsecure connection.
	Port = 2404

	// PortSecure is the IANA registered port number for secure connection.
	PortSecure = 19998
)

// defines an IEC 60870-5-104 configuration range
const (
	// "t₀" range [1, 255]s, default 30s.
	ConnectTimeout0Min = 1 * time.Second
	ConnectTimeout0Max = 255 * time.Second

	// "t₁" range [1, 255]s, default 15s. See IEC 60870-5-104, figure 18.
	SendUnAckTimeout1Min = 1 * time.Second
	SendUnAckTimeout1Max = 255 * time.Second

	// "t₂" range [1, 255]s, default 10s. See IEC 60870-5-104, figure 10.
	RecvUnAckTimeout2Min = 1 * time.Second
	RecvUnAckTimeout2Max = 255 * time.Second

	// "t₃" range [1 second, 48 hours], default 20s. See IEC 60870-5-104, subclass 5.2.
	IdleTimeout3Min = 1 * time.Second
	IdleTimeout3Max = 48 * time.Hour

	// "k" range [1, 32767], default 12. See IEC 60870-5-104, subclass 5.5.
	SendUnAckLimitKMin = 1
	SendUnAckLimitKMax = 32767

	// "w" range [1, 32767], default 8. See IEC 60870-5-104, subclass 5.5.
	RecvUnAckLimitWMin = 1
	RecvUnAckLimitWMax = 32767
)

// Config defines an IEC 60870-5-104 configuration.
// The default is applied for each unspecified value.
type Config struct {
	// Maximum time allowed to establish the TCP connection.
	// "t₀" range [1, 255]s, default 30s.
	ConnectTimeout0 time.Duration

	// Maximum number of I-frames sent without acknowledgement. On reaching
	// it, transmission stops until the peer acknowledges.
	// "k" range [1, 32767], default 12.
	// See IEC 60870-5-104, subclass 5.5.
	SendUnAckLimitK uint16

	// Longest a sent frame may go unacknowledged. On expiry the connection
	// is closed immediately.
	// "t₁" range [1, 255]s, default 15s.
	// See IEC 60870-5-104, figure 18.
	SendUnAckTimeout1 time.Duration

	// The receiver must acknowledge at the latest after receiving w I-frame
	// APDUs. w must not exceed 2/3 k (2/3 of SendUnAckLimitK).
	// "w" range [1, 32767], default 8.
	// See IEC 60870-5-104, subclass 5.5.
	RecvUnAckLimitW uint16

	// Longest an acknowledgement may be deferred. Reaching "w" frames
	// acknowledges sooner; this timeout covers the case where the peer stops
	// sending before "w" frames have accumulated.
	// "t₂" range [1, 255]s, default 10s. Must be less than "t₁".
	// See IEC 60870-5-104, figure 10.
	RecvUnAckTimeout2 time.Duration

	// Idle time that triggers a "TESTFR" keep-alive.
	// "t₃" range [1 second, 48 hours], default 20s.
	// See IEC 60870-5-104, subclass 5.2.
	IdleTimeout3 time.Duration
}

// Valid applies the default (defined by IEC) for each unspecified value.
func (sf *Config) Valid() error {
	if sf == nil {
		return errors.New("invalid pointer")
	}

	if sf.ConnectTimeout0 == 0 {
		sf.ConnectTimeout0 = 30 * time.Second
	} else if sf.ConnectTimeout0 < ConnectTimeout0Min || sf.ConnectTimeout0 > ConnectTimeout0Max {
		return errors.New(`ConnectTimeout0 "t₀" not in [1, 255]s`)
	}

	if sf.SendUnAckLimitK == 0 {
		sf.SendUnAckLimitK = 12
	} else if sf.SendUnAckLimitK < SendUnAckLimitKMin || sf.SendUnAckLimitK > SendUnAckLimitKMax {
		return errors.New(`SendUnAckLimitK "k" not in [1, 32767]`)
	}

	if sf.SendUnAckTimeout1 == 0 {
		sf.SendUnAckTimeout1 = 15 * time.Second
	} else if sf.SendUnAckTimeout1 < SendUnAckTimeout1Min || sf.SendUnAckTimeout1 > SendUnAckTimeout1Max {
		return errors.New(`SendUnAckTimeout1 "t₁" not in [1, 255]s`)
	}

	// "w" defaults to 2/3 "k" rather than to a fixed 8, so that lowering "k"
	// alone still yields a valid pair. At the default k=12 this is 8, the
	// value the standard names and the one this defaulted to before.
	if sf.RecvUnAckLimitW == 0 {
		sf.RecvUnAckLimitW = max(sf.SendUnAckLimitK*2/3, RecvUnAckLimitWMin)
	} else if sf.RecvUnAckLimitW < RecvUnAckLimitWMin || sf.RecvUnAckLimitW > RecvUnAckLimitWMax {
		return errors.New(`RecvUnAckLimitW "w" not in [1, 32767]`)
	} else if sf.RecvUnAckLimitW > sf.SendUnAckLimitK*2/3 {
		// Subclass 5.5: the receiver must acknowledge at the latest after w
		// APDUs, and the sender stops after k unacknowledged ones. Letting w
		// approach (or exceed) k means the sender routinely stalls waiting
		// for an acknowledgement the receiver is not yet obliged to send, so
		// throughput collapses to whatever t₂ paces -- a failure that looks
		// like a slow link rather than a misconfiguration.
		return errors.New(`RecvUnAckLimitW "w" must not exceed 2/3 of SendUnAckLimitK "k"`)
	}

	if sf.RecvUnAckTimeout2 == 0 {
		sf.RecvUnAckTimeout2 = 10 * time.Second
	} else if sf.RecvUnAckTimeout2 < RecvUnAckTimeout2Min || sf.RecvUnAckTimeout2 > RecvUnAckTimeout2Max {
		return errors.New(`RecvUnAckTimeout2 "t₂" not in [1, 255]s`)
	}
	if sf.RecvUnAckTimeout2 >= sf.SendUnAckTimeout1 {
		// t₂ is when this end acknowledges received frames; t₁ is when the
		// peer gives up waiting for that acknowledgement and drops the link.
		// t₂ >= t₁ means the acknowledgement is always sent too late, so a
		// quiet link disconnects on a timer instead of staying up -- the
		// connection cycling is periodic and gives no hint of its cause.
		return errors.New(`RecvUnAckTimeout2 "t₂" must be less than SendUnAckTimeout1 "t₁"`)
	}

	if sf.IdleTimeout3 == 0 {
		sf.IdleTimeout3 = 20 * time.Second
	} else if sf.IdleTimeout3 < IdleTimeout3Min || sf.IdleTimeout3 > IdleTimeout3Max {
		return errors.New(`IdleTimeout3 "t₃" not in [1 second, 48 hours]`)
	}

	return nil
}

// DefaultConfig default config
func DefaultConfig() Config {
	return Config{
		ConnectTimeout0:   30 * time.Second,
		SendUnAckLimitK:   12,
		SendUnAckTimeout1: 15 * time.Second,
		RecvUnAckLimitW:   8,
		RecvUnAckTimeout2: 10 * time.Second,
		IdleTimeout3:      20 * time.Second,
	}
}
