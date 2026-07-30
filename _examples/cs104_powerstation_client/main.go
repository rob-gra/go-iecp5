// Command cs104_powerstation_client demonstrates cs104.Client driving the
// cs104_powerstation_server demo: it issues a general interrogation, sets
// an output setpoint, starts the station, and later stops it, logging every
// measurement and status update it receives along the way.
package main

import (
	"fmt"
	"log"
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
)

func main() {
	option := cs104.NewOption()
	if err := option.AddRemoteServer("127.0.0.1:2404"); err != nil {
		panic(err)
	}

	client := cs104.NewClient(&handler{}, option)
	client.LogMode(true)

	client.SetOnConnectHandler(func(c *cs104.Client) {
		log.Println("connected, sending STARTDT")
		c.SendStartDt()
	})
	client.SetConnectionLostHandler(func(c *cs104.Client) {
		log.Println("connection lost, will auto-reconnect")
	})

	if err := client.Start(); err != nil {
		panic(err)
	}

	// STARTDT is asynchronous; give the station a moment to confirm it
	// before issuing commands.
	time.Sleep(2 * time.Second)

	if err := client.InterrogationCmd(asdu.CauseOfTransmission{Cause: asdu.Activation}, asdu.GlobalCommonAddr, asdu.QOIStation); err != nil {
		log.Printf("interrogation command failed: %v", err)
	}

	time.Sleep(2 * time.Second)
	log.Println("setting output setpoint to 75%")
	if err := asdu.SetpointCmdFloat(client, asdu.C_SE_NC_1, asdu.CauseOfTransmission{Cause: asdu.Activation}, asdu.GlobalCommonAddr,
		asdu.SetpointCommandFloatInfo{Ioa: ioaSetpointCmd, Value: 75}); err != nil {
		log.Printf("setpoint command failed: %v", err)
	}

	log.Println("starting the station")
	if err := asdu.SingleCmd(client, asdu.C_SC_NA_1, asdu.CauseOfTransmission{Cause: asdu.Activation}, asdu.GlobalCommonAddr,
		asdu.SingleCommandInfo{Ioa: ioaStartStopCmd, Value: true}); err != nil {
		log.Printf("start command failed: %v", err)
	}

	time.Sleep(30 * time.Second)
	log.Println("stopping the station")
	if err := asdu.SingleCmd(client, asdu.C_SC_NA_1, asdu.CauseOfTransmission{Cause: asdu.Activation}, asdu.GlobalCommonAddr,
		asdu.SingleCommandInfo{Ioa: ioaStartStopCmd, Value: false}); err != nil {
		log.Printf("stop command failed: %v", err)
	}

	select {}
}

// handler implements cs104.ClientHandlerInterface.
type handler struct{}

func (handler) InterrogationHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("interrogation response: %s", a.Identifier)
	return nil
}
func (handler) CounterInterrogationHandler(_ asdu.Connect, a *asdu.ASDU) error { return nil }
func (handler) ReadHandler(_ asdu.Connect, a *asdu.ASDU) error                 { return nil }
func (handler) TestCommandHandler(_ asdu.Connect, a *asdu.ASDU) error          { return nil }
func (handler) ClockSyncHandler(_ asdu.Connect, a *asdu.ASDU) error            { return nil }
func (handler) ResetProcessHandler(_ asdu.Connect, a *asdu.ASDU) error         { return nil }
func (handler) DelayAcquisitionHandler(_ asdu.Connect, a *asdu.ASDU) error     { return nil }

// ASDUHandler logs process data from the station: the two measurements
// (generator output, reservoir level) and the running status.
func (handler) ASDUHandler(_ asdu.Connect, a *asdu.ASDU) error {
	switch a.Identifier.Type {
	case asdu.M_ME_NC_1, asdu.M_ME_TF_1:
		for _, v := range a.GetMeasuredValueFloat() {
			log.Printf("%s = %.1f%%", pointName(v.Ioa), v.Value)
		}
	case asdu.M_SP_NA_1, asdu.M_SP_TB_1:
		for _, v := range a.GetSinglePoint() {
			log.Printf("%s = %v", pointName(v.Ioa), v.Value)
		}
	default:
		log.Printf("received: %s", a.Identifier)
	}
	return nil
}

func pointName(ioa asdu.InfoObjAddr) string {
	switch ioa {
	case ioaGeneratorOutput:
		return "generator output"
	case ioaReservoirLevel:
		return "reservoir level"
	case ioaStationRunning:
		return "station running"
	default:
		return fmt.Sprintf("IOA %d", ioa)
	}
}
