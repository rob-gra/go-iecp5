// Command cs104_server_special demonstrates cs104.ServerSpecial: a slave/RTU
// that dials out to the master instead of listening for it, e.g. because
// the RTU sits behind NAT/firewall and can't accept inbound connections.
// Wire protocol behavior (STARTDT, interrogation, ...) is identical to
// cs104.Server; only who initiates the TCP connection differs.
package main

import (
	"log"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/cs104"
)

func main() {
	option := cs104.NewOption()
	if err := option.AddRemoteServer("127.0.0.1:2404"); err != nil {
		panic(err)
	}

	srv := cs104.NewServerSpecial(&handler{}, option)
	srv.LogMode(true)

	srv.SetOnConnectHandler(func(c asdu.Connect) {
		log.Println("dialed out to master")
	})
	srv.SetConnectionLostHandler(func(c asdu.Connect) {
		log.Println("disconnected, will auto-reconnect")
	})

	if err := srv.Start(); err != nil {
		panic(err)
	}

	select {}
}

// handler implements cs104.ServerHandlerInterface. See cs104_server for a
// fuller interrogation-handling example.
type handler struct{}

func (handler) InterrogationHandler(c asdu.Connect, req *asdu.ASDU, qoi asdu.QualifierOfInterrogation) error {
	log.Printf("interrogation requested, qoi=%d", qoi)

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
