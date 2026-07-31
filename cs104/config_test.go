// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"testing"
	"time"
)

// The IEC-recommended defaults must satisfy every rule Valid enforces --
// otherwise the library's own starting point is a configuration it rejects.
func TestConfig_DefaultConfigIsValid(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Valid(); err != nil {
		t.Fatalf("DefaultConfig() is not valid: %v", err)
	}
	if cfg != DefaultConfig() {
		t.Fatalf("Valid() modified an already-complete DefaultConfig(): %+v", cfg)
	}
}

// An empty Config means "use the defaults for everything", so it must
// normalize to exactly DefaultConfig().
func TestConfig_ZeroValueNormalizesToDefaults(t *testing.T) {
	var cfg Config
	if err := cfg.Valid(); err != nil {
		t.Fatalf("zero Config: %v", err)
	}
	if cfg != DefaultConfig() {
		t.Fatalf("zero Config normalized to %+v, want DefaultConfig() %+v", cfg, DefaultConfig())
	}
}

// Lowering k alone must still produce a valid pair: w defaults relative to
// k, so it can't be left at a fixed 8 that the new k no longer permits.
func TestConfig_UnspecifiedWFollowsK(t *testing.T) {
	for _, k := range []uint16{1, 3, 6, 12, 30, 32767} {
		cfg := Config{SendUnAckLimitK: k}
		if err := cfg.Valid(); err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		if cfg.RecvUnAckLimitW > k*2/3 && cfg.RecvUnAckLimitW != RecvUnAckLimitWMin {
			t.Errorf("k=%d defaulted w=%d, want <= 2/3k (%d)", k, cfg.RecvUnAckLimitW, k*2/3)
		}
		if cfg.RecvUnAckLimitW < RecvUnAckLimitWMin {
			t.Errorf("k=%d defaulted w=%d, below the minimum of %d",
				k, cfg.RecvUnAckLimitW, RecvUnAckLimitWMin)
		}
	}
}

// Both constraints are documented on the Config fields themselves, and both
// cause failures that look like anything other than a misconfiguration --
// w > 2/3k throttles throughput to t₂, t₂ >= t₁ cycles the connection.
func TestConfig_RejectsInconsistentPairs(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "w exceeds 2/3 k",
			cfg:  Config{SendUnAckLimitK: 12, RecvUnAckLimitW: 9},
		},
		{
			name: "w equals k",
			cfg:  Config{SendUnAckLimitK: 12, RecvUnAckLimitW: 12},
		},
		{
			name: "t2 equals t1",
			cfg:  Config{SendUnAckTimeout1: 15 * time.Second, RecvUnAckTimeout2: 15 * time.Second},
		},
		{
			name: "t2 exceeds t1",
			cfg:  Config{SendUnAckTimeout1: 15 * time.Second, RecvUnAckTimeout2: 20 * time.Second},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Valid(); err == nil {
				t.Fatalf("Valid() accepted %+v, want an error", tt.cfg)
			}
		})
	}
}

// A rejected config must not be silently adopted: SetConfig falls back to
// the defaults, which is only safe if the caller can tell it happened.
func TestServer_SetConfigFallsBackOnInvalid(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.SetConfig(Config{SendUnAckTimeout1: 15 * time.Second, RecvUnAckTimeout2: 20 * time.Second})

	if srv.config != DefaultConfig() {
		t.Fatalf("invalid config was adopted: %+v", srv.config)
	}
}
