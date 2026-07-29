// Command cs104_server demonstrates cs104.Server, the IEC 60870-5-104
// slave/RTU side: it listens for incoming master connections and responds
// to a general interrogation with one point of data.
package main

import (
	"log"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/cs104"
)

func main() {
	srv := cs104.NewServer(&handler{})
	srv.SetOnConnectionHandler(func(c asdu.Connect) {
		log.Println("master connected")
	})
	srv.SetConnectionLostHandler(func(c asdu.Connect) {
		log.Println("master disconnected")
	})
	srv.LogMode(true)

	srv.ListenAndServer(":2404")
}

// handler implements cs104.ServerHandlerInterface.
type handler struct{}

// InterrogationHandler responds to a general interrogation (C_IC_NA_1) by
// confirming activation, reporting the current value of one single point,
// then confirming termination, per IEC 60870-5-104 subclass 5.6.
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
