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
	// a time. Activating a connection (STARTDT) closes any other connection
	// that was previously active, matching the "one active master" model
	// IEC 60870-5-104 redundancy is intended for.
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
		host := conn.RemoteAddr().String()
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
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
// redundancy group, any other still-active session in the same group is
// closed, since only one connection per group may be active at a time.
func (sf *Server) handleSessionActivated(activated *SrvSession) {
	if activated.redundancyGroupKey == nil {
		return
	}

	sf.mux.Lock()
	var superseded []*SrvSession
	for sess := range sf.sessions {
		if sess == activated || sess.redundancyGroupKey != activated.redundancyGroupKey {
			continue
		}
		if sess.IsActive() {
			superseded = append(superseded, sess)
		}
	}
	sf.mux.Unlock()

	for _, sess := range superseded {
		sf.Debug("closing connection: superseded by a newly active connection in the same redundancy group")
		// Hand off whatever the superseded connection hadn't sent yet to the
		// connection that's replacing it, so closing it doesn't lose data.
		sess.sendQueue.DrainTo(activated.sendQueue)
		sess.forceClose()
	}
}
