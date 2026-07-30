// Command cs104_powerstation_server demonstrates a small, realistic
// cs104.Server application: a simulated power station with a generator fed
// by a reservoir. Run it alongside cs104_powerstation_client for
// interactive testing.
//
// Process data reported (monitoring direction):
//   - IOA 1: generator output, 0-100% of capacity (M_ME_NC_1/M_ME_TF_1)
//   - IOA 2: reservoir level, 0-100% full (M_ME_NC_1/M_ME_TF_1)
//   - IOA 3: station running (M_SP_NA_1/M_SP_TB_1)
//
// Commands accepted (control direction): the library only dispatches
// system/interrogation ASDUs itself (see cs104.ServerHandlerInterface), so
// process commands are inspected and applied directly in ASDUHandler.
//   - IOA 20: start/stop the station (C_SC_NA_1, single command)
//   - IOA 21: output setpoint, 0-100% (C_SE_NC_1, set-point command,
//     short floating point)
//
// The generator only produces output while running, ramping toward the
// current setpoint and draining the reservoir proportionally to output; the
// reservoir slowly refills while stopped. If the reservoir empties, output
// drops to 0 regardless of setpoint.
package main

import (
	"log"
	"sync"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/cs104"
)

const (
	ioaGeneratorOutput asdu.InfoObjAddr = 1
	ioaReservoirLevel  asdu.InfoObjAddr = 2
	ioaStationRunning  asdu.InfoObjAddr = 3
	ioaStartStopCmd    asdu.InfoObjAddr = 20
	ioaSetpointCmd     asdu.InfoObjAddr = 21

	tickInterval = time.Second

	rampRate   = float32(8)    // max change in output per tick, percentage points
	drainRate  = float32(0.15) // reservoir % consumed per 1% of output, per tick
	refillRate = float32(1.5)  // reservoir % regained per tick while stopped
)

func main() {
	st := newStation()
	h := &handler{station: st}

	srv := cs104.NewServer(h)
	srv.SetOnConnectionHandler(func(c asdu.Connect) {
		log.Println("master connected")
	})
	srv.SetConnectionLostHandler(func(c asdu.Connect) {
		log.Println("master disconnected")
	})
	srv.LogMode(true)

	go reportLoop(srv, st)

	srv.ListenAndServer(":2404")
}

// reportLoop advances the simulation once per tick and reports it as
// spontaneous process data: the two measurements every tick (continuous
// telemetry), and the running status only when it actually changes (an
// edge-triggered status point, unlike the continuous measurements).
func reportLoop(c asdu.Connect, st *station) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	wasRunning, _, _ := st.snapshot()
	for range ticker.C {
		running, output, reservoir := st.tick()
		cause := asdu.CauseOfTransmission{Cause: asdu.Spontaneous}
		now := time.Now()

		if err := asdu.MeasuredValueFloatCP56Time2a(c, cause, asdu.GlobalCommonAddr,
			asdu.MeasuredValueFloatInfo{Ioa: ioaGeneratorOutput, Value: output, Qds: asdu.QDSGood, Time: now}); err != nil {
			log.Printf("send generator output failed: %v", err)
		}
		if err := asdu.MeasuredValueFloatCP56Time2a(c, cause, asdu.GlobalCommonAddr,
			asdu.MeasuredValueFloatInfo{Ioa: ioaReservoirLevel, Value: reservoir, Qds: asdu.QDSGood, Time: now}); err != nil {
			log.Printf("send reservoir level failed: %v", err)
		}

		if running != wasRunning {
			wasRunning = running
			if err := asdu.SingleCP56Time2a(c, cause, asdu.GlobalCommonAddr,
				asdu.SinglePointInfo{Ioa: ioaStationRunning, Value: running, Qds: asdu.QDSGood, Time: now}); err != nil {
				log.Printf("send running status failed: %v", err)
			}
		}
	}
}

// station simulates the power plant's physical state.
type station struct {
	mu        sync.Mutex
	running   bool
	setpoint  float32 // target output, 0-100%
	output    float32 // current output, 0-100%
	reservoir float32 // current level, 0-100%
}

func newStation() *station {
	return &station{reservoir: 100}
}

// setRunning starts or stops the station and reports whether the running
// state actually changed.
func (s *station) setRunning(run bool) (changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed = s.running != run
	s.running = run
	return changed
}

func (s *station) setSetpoint(pct float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setpoint = clamp(pct, 0, 100)
}

// tick advances the simulation by one time step and returns the resulting
// state.
func (s *station) tick() (running bool, output, reservoir float32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	target := float32(0)
	if s.running && s.reservoir > 0 {
		target = s.setpoint
	}
	s.output = step(s.output, target, rampRate)

	if s.running {
		s.reservoir = clamp(s.reservoir-s.output*drainRate/100, 0, 100)
		if s.reservoir == 0 {
			s.output = 0
		}
	} else {
		s.reservoir = clamp(s.reservoir+refillRate, 0, 100)
	}

	return s.running, s.output, s.reservoir
}

// snapshot returns the current state without advancing the simulation.
func (s *station) snapshot() (running bool, output, reservoir float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.output, s.reservoir
}

func clamp(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// step moves cur toward target by at most maxDelta.
func step(cur, target, maxDelta float32) float32 {
	switch {
	case cur < target:
		if next := cur + maxDelta; next < target {
			return next
		}
		return target
	case cur > target:
		if next := cur - maxDelta; next > target {
			return next
		}
		return target
	default:
		return cur
	}
}

// handler implements cs104.ServerHandlerInterface, reporting station state
// on interrogation and applying start/stop and setpoint commands.
type handler struct {
	station *station
}

func (h *handler) InterrogationHandler(c asdu.Connect, req *asdu.ASDU, qoi asdu.QualifierOfInterrogation) error {
	if err := req.SendReplyMirror(c, asdu.ActivationCon); err != nil {
		return err
	}

	running, output, reservoir := h.station.snapshot()
	cause := asdu.CauseOfTransmission{Cause: asdu.InterrogatedByStation}
	if err := asdu.Single(c, false, cause, asdu.GlobalCommonAddr,
		asdu.SinglePointInfo{Ioa: ioaStationRunning, Value: running, Qds: asdu.QDSGood}); err != nil {
		log.Printf("send running status failed: %v", err)
	}
	if err := asdu.MeasuredValueFloat(c, false, cause, asdu.GlobalCommonAddr,
		asdu.MeasuredValueFloatInfo{Ioa: ioaGeneratorOutput, Value: output, Qds: asdu.QDSGood}); err != nil {
		log.Printf("send generator output failed: %v", err)
	}
	if err := asdu.MeasuredValueFloat(c, false, cause, asdu.GlobalCommonAddr,
		asdu.MeasuredValueFloatInfo{Ioa: ioaReservoirLevel, Value: reservoir, Qds: asdu.QDSGood}); err != nil {
		log.Printf("send reservoir level failed: %v", err)
	}
	return req.SendReplyMirror(c, asdu.ActivationTerm)
}

func (h *handler) CounterInterrogationHandler(asdu.Connect, *asdu.ASDU, asdu.QualifierCountCall) error {
	return nil
}
func (h *handler) ReadHandler(asdu.Connect, *asdu.ASDU, asdu.InfoObjAddr) error { return nil }
func (h *handler) ClockSyncHandler(asdu.Connect, *asdu.ASDU, time.Time) error   { return nil }
func (h *handler) ResetProcessHandler(asdu.Connect, *asdu.ASDU, asdu.QualifierOfResetProcessCmd) error {
	return nil
}
func (h *handler) DelayAcquisitionHandler(asdu.Connect, *asdu.ASDU, uint16) error { return nil }

// ASDUHandler receives process commands. Recognized commands reply and
// apply themselves, returning nil; anything else returns an error so
// serverHandler replies UnknownTypeID on our behalf (see cs104's
// serverHandler for that fallback).
func (h *handler) ASDUHandler(c asdu.Connect, asduPack *asdu.ASDU) error {
	switch asduPack.Identifier.Type {
	case asdu.C_SC_NA_1: // start/stop
		cmd, err := asduPack.GetSingleCmd()
		if err != nil {
			return asduPack.SendReplyMirror(c, asdu.UnknownIOA)
		}
		if cmd.Ioa != ioaStartStopCmd {
			return asduPack.SendReplyMirror(c, asdu.UnknownIOA)
		}
		if err := asduPack.SendReplyMirror(c, asdu.ActivationCon); err != nil {
			return err
		}
		if h.station.setRunning(cmd.Value) {
			log.Printf("station %s", runningWord(cmd.Value))
		}
		return asduPack.SendReplyMirror(c, asdu.ActivationTerm)

	case asdu.C_SE_NC_1: // output setpoint
		cmd, err := asduPack.GetSetpointFloatCmd()
		if err != nil {
			return asduPack.SendReplyMirror(c, asdu.UnknownIOA)
		}
		if cmd.Ioa != ioaSetpointCmd {
			return asduPack.SendReplyMirror(c, asdu.UnknownIOA)
		}
		if err := asduPack.SendReplyMirror(c, asdu.ActivationCon); err != nil {
			return err
		}
		h.station.setSetpoint(cmd.Value)
		log.Printf("setpoint changed to %.1f%%", cmd.Value)
		return asduPack.SendReplyMirror(c, asdu.ActivationTerm)
	}

	return asdu.ErrTypeIdentifier
}

func runningWord(running bool) string {
	if running {
		return "started"
	}
	return "stopped"
}
