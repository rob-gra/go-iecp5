// Command cs104_server demonstrates cs104.Server, the IEC 60870-5-104
// slave/RTU side: it listens for incoming master connections, responds to a
// general interrogation with one point of data, and periodically reports a
// few Go runtime statistics as process data (see reportRuntimeMetrics).
package main

import (
	"log"
	"runtime"
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

	// Server itself implements asdu.Connect, broadcasting to every
	// currently connected master.
	go reportRuntimeMetrics(srv)

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

// reportRuntimeMetrics periodically publishes a few Go runtime statistics as
// spontaneous IEC 60870-5-104 process data, useful as a self-monitoring
// signal for the RTU process itself:
//
//   - IOA 10: heap memory in use, in MiB -- type 36, M_ME_TF_1 (measured
//     value, short floating point number, with a CP56Time2a time tag).
//   - IOA 11: number of goroutines -- also M_ME_TF_1. Both are continuous
//     measurements, so a floating point type fits naturally.
//   - IOA 12: whether a GC cycle has run since the last report -- type 30,
//     M_SP_TB_1 (single point information, with a CP56Time2a time tag).
//     Most runtime stats are continuous measurements, not naturally
//     boolean, but "did a GC happen in this interval" is a clean fit for a
//     single (on/off) indication.
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
