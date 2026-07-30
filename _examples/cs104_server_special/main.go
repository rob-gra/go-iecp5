// Command cs104_server_special demonstrates cs104.ServerSpecial: a slave/RTU
// that dials out to the master instead of listening for it, e.g. because
// the RTU sits behind NAT/firewall and can't accept inbound connections.
// Wire protocol behavior (STARTDT, interrogation, ...) is identical to
// cs104.Server; only who initiates the TCP connection differs.
package main

import (
	"log"
	"runtime"
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

	// ServerSpecial itself implements asdu.Connect.
	go reportRuntimeMetrics(srv)

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

// reportRuntimeMetrics periodically publishes a few Go runtime statistics as
// spontaneous IEC 60870-5-104 process data. See cs104_server's
// reportRuntimeMetrics for the full rationale behind each point's type and
// IOA (heap in use and goroutine count as type 36 M_ME_TF_1, whether a GC
// ran since the last report as type 30 M_SP_TB_1).
func reportRuntimeMetrics(c asdu.Connect) {
	var lastNumGC uint32

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		now := time.Now()
		cause := asdu.CauseOfTransmission{Cause: asdu.Spontaneous}

		if err := asdu.MeasuredValueFloatCP56Time2a(c, cause, asdu.GlobalCommonAddr,
			asdu.MeasuredValueFloatInfo{Ioa: 10, Value: float32(mem.HeapAlloc) / (1 << 20), Qds: asdu.QDSGood, Time: now}); err != nil {
			log.Printf("send heap-in-use metric failed: %v", err)
		}
		if err := asdu.MeasuredValueFloatCP56Time2a(c, cause, asdu.GlobalCommonAddr,
			asdu.MeasuredValueFloatInfo{Ioa: 11, Value: float32(runtime.NumGoroutine()), Qds: asdu.QDSGood, Time: now}); err != nil {
			log.Printf("send goroutine-count metric failed: %v", err)
		}

		gcRanSinceLastReport := mem.NumGC != lastNumGC
		lastNumGC = mem.NumGC
		if err := asdu.SingleCP56Time2a(c, cause, asdu.GlobalCommonAddr,
			asdu.SinglePointInfo{Ioa: 12, Value: gcRanSinceLastReport, Qds: asdu.QDSGood, Time: now}); err != nil {
			log.Printf("send gc-occurred indication failed: %v", err)
		}
	}
}
