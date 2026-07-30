// Command cs104_server_redundancy demonstrates cs104.Server configured for
// redundant masters: IEC 60870-5-104 redundancy expects only one master to
// be actively receiving data at a time, with the others kept connected as
// warm standbys so they can take over without paying reconnection cost.
// With ModeSingleRedundancyGroup, when a second master connects and sends
// STARTDT, the server deactivates whichever connection was previously
// active in the group (an unsolicited STOPDT confirm; its TCP connection is
// left open), and hands off any of its not-yet-sent outbound messages to
// the connection replacing it (see cs104.RedundancyGroup for grouping by
// client IP instead, under ModeMultipleRedundancyGroups).
package main

import (
	"log"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/cs104"
)

func main() {
	srv := cs104.NewServer(&handler{})
	srv.SetServerMode(cs104.ModeSingleRedundancyGroup)

	// For per-group control instead, use ModeMultipleRedundancyGroups and
	// register the groups the connecting clients' IPs belong to:
	//
	//   srv.SetServerMode(cs104.ModeMultipleRedundancyGroups)
	//   srv.AddRedundancyGroup(cs104.NewRedundancyGroup("control-room").
	//       AddAllowedClient("192.168.1.10").
	//       AddAllowedClient("192.168.1.11"))

	srv.SetOnConnectionHandler(func(c asdu.Connect) {
		log.Println("master connected")
	})
	srv.SetConnectionLostHandler(func(c asdu.Connect) {
		// Note: a master superseded by another active master in its
		// redundancy group is deactivated, not disconnected, so this only
		// fires for a connection that actually dropped.
		log.Println("master disconnected")
	})
	srv.LogMode(true)

	srv.ListenAndServer(":2404")
}

// handler implements cs104.ServerHandlerInterface. See cs104_server for a
// fuller interrogation-handling example; redundancy-group behavior is
// entirely independent of what the handler does.
type handler struct{}

func (handler) InterrogationHandler(c asdu.Connect, req *asdu.ASDU, qoi asdu.QualifierOfInterrogation) error {
	if err := req.SendReplyMirror(c, asdu.ActivationCon); err != nil {
		return err
	}
	if err := asdu.Single(c, false, asdu.CauseOfTransmission{Cause: asdu.InterrogatedByStation}, asdu.GlobalCommonAddr,
		asdu.SinglePointInfo{Ioa: 1, Value: true, Qds: asdu.QDSGood, Time: time.Now()}); err != nil {
		log.Printf("send single point info failed: %v", err)
	}
	return req.SendReplyMirror(c, asdu.ActivationTerm)
}

func (handler) CounterInterrogationHandler(asdu.Connect, *asdu.ASDU, asdu.QualifierCountCall) error {
	return nil
}
func (handler) ReadHandler(asdu.Connect, *asdu.ASDU, asdu.InfoObjAddr) error { return nil }
func (handler) ClockSyncHandler(asdu.Connect, *asdu.ASDU, time.Time) error   { return nil }
func (handler) ResetProcessHandler(asdu.Connect, *asdu.ASDU, asdu.QualifierOfResetProcessCmd) error {
	return nil
}
func (handler) DelayAcquisitionHandler(asdu.Connect, *asdu.ASDU, uint16) error { return nil }
func (handler) ASDUHandler(asdu.Connect, *asdu.ASDU) error                     { return nil }
