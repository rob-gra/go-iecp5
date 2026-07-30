// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import "net"

// ServerMode selects how Server groups connections together for redundancy
// enforcement. See Server.SetServerMode.
type ServerMode int

const (
	// ModeConnectionIsRedundancyGroup is the default: every connection is
	// independent, and one connection activating (STARTDT) has no effect on
	// any other connection.
	ModeConnectionIsRedundancyGroup ServerMode = iota

	// ModeSingleRedundancyGroup treats every connection to this server as
	// members of one redundancy group: only one connection may be active at
	// a time. Activating a connection (STARTDT) deactivates any other
	// connection that was previously active in the group -- it falls back
	// to STOPDT but stays connected as a standby -- matching the "one
	// active master, others as warm standbys" model IEC 60870-5-104
	// redundancy is intended for.
	ModeSingleRedundancyGroup

	// ModeMultipleRedundancyGroups groups connections by the RedundancyGroup
	// whose allowed client list contains the connecting peer's IP address,
	// see Server.AddRedundancyGroup. A connection whose peer IP matches no
	// registered group is left ungrouped: equivalent to
	// ModeConnectionIsRedundancyGroup for that one connection.
	ModeMultipleRedundancyGroups
)

// RedundancyGroup names a set of client IP addresses that should be treated
// as each other's failover peers under ModeMultipleRedundancyGroups: only
// one connection from the group may be active at a time.
type RedundancyGroup struct {
	Name    string
	allowed map[string]struct{}
}

// NewRedundancyGroup creates a named, initially empty redundancy group. Add
// member clients with AddAllowedClient, then register it with
// Server.AddRedundancyGroup.
func NewRedundancyGroup(name string) *RedundancyGroup {
	return &RedundancyGroup{Name: name, allowed: make(map[string]struct{})}
}

// AddAllowedClient adds a client IP address (e.g. "192.168.1.10") as a
// member of this redundancy group.
func (rg *RedundancyGroup) AddAllowedClient(ip string) *RedundancyGroup {
	rg.allowed[ip] = struct{}{}
	return rg
}

func (rg *RedundancyGroup) matches(ip string) bool {
	_, ok := rg.allowed[ip]
	return ok
}

// singleRedundancyGroupKey is the shared redundancy-group key given to every
// connection under ModeSingleRedundancyGroup: being a zero-size struct type,
// all its values compare equal, so it works directly as a comparable
// interface{} map/group key without needing a sentinel value.
type singleRedundancyGroupKey struct{}

// groupKeyFor returns the redundancy-group key a newly accepted connection
// belongs to, or nil if it isn't grouped with any other connection. Sessions
// sharing a non-nil, equal key are each other's failover peers: see
// Server.handleSessionActivated.
func (sf *Server) groupKeyFor(conn net.Conn) interface{} {
	switch sf.serverMode {
	case ModeSingleRedundancyGroup:
		return singleRedundancyGroupKey{}
	case ModeMultipleRedundancyGroups:
		host := hostOnly(conn.RemoteAddr())
		for _, rg := range sf.redundancyGroups {
			if rg.matches(host) {
				return rg
			}
		}
		return nil
	default: // ModeConnectionIsRedundancyGroup
		return nil
	}
}

// handleSessionActivated is invoked (via SrvSession.onActivate) whenever a
// session transitions from inactive to active. If the session belongs to a
// redundancy group, whichever session was previously recorded as that
// group's active connection is deactivated, since only one connection per
// group may be active at a time. Per IEC 60870-5-104's redundant-connection
// model, the superseded connection is not closed: it falls back to inactive
// (STOPDT) and stays connected as a standby, ready to be reactivated later
// without paying reconnection cost.
//
// activeByGroup (swapped under sf.mux) is the single source of truth for
// "who is active in this group" -- deliberately not re-derived by scanning
// sf.sessions for IsActive() sessions on every call. Two sessions in the
// same group can call onActivate concurrently (e.g. two masters both
// issuing STARTDT within microseconds of each other during a failover);
// re-deriving from each session's own IsActive() flag lets both calls see
// the other as "the one to supersede," and both end up deactivated, leaving
// the group with no active connection at all. Swapping a single map entry
// under one lock instead means whichever call acquires the lock second
// always sees itself already recorded as active by the first, so exactly
// one of the two is ever told to deactivate.
func (sf *Server) handleSessionActivated(activated *SrvSession) {
	if activated.redundancyGroupKey == nil {
		return
	}

	sf.mux.Lock()
	prev := sf.activeByGroup[activated.redundancyGroupKey]
	sf.activeByGroup[activated.redundancyGroupKey] = activated
	sf.mux.Unlock()

	if prev == nil || prev == activated {
		return
	}
	// prev is the session this one supersedes, but it may have gone inactive
	// on its own in the meantime -- it processed a STOPDT from its peer, or
	// its connection dropped. There's nothing to supersede in that case, and
	// deactivating it anyway would send a redundant unsolicited STOPDT
	// confirm to a peer that already stopped. Note this only gates *whether*
	// we signal the session picked above; it never re-derives *which* one,
	// which is what made the concurrent-activation race possible.
	if !prev.IsActive() {
		return
	}

	sf.Debug("deactivating connection: superseded by a newly active connection in the same redundancy group")
	// Hand off whatever the superseded connection hadn't sent yet to the
	// connection that's replacing it, since it won't be transmitting
	// anymore once deactivated.
	prev.sendQueue.DrainTo(activated.sendQueue)
	prev.forceDeactivate()
}
