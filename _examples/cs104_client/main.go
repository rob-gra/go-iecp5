// Command cs104_client demonstrates cs104.Client, the IEC 60870-5-104
// master: it dials out to a slave/RTU, activates data transfer, and issues
// a general interrogation to request its current data set.
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

	// STARTDT is asynchronous; give the slave a moment to confirm it before
	// issuing commands. A real application would typically trigger this
	// from application logic rather than a fixed sleep.
	time.Sleep(2 * time.Second)

	if err := client.InterrogationCmd(asdu.CauseOfTransmission{Cause: asdu.Activation}, asdu.GlobalCommonAddr, asdu.QOIStation); err != nil {
		log.Printf("interrogation command failed: %v", err)
	}

	select {}
}

// handler implements cs104.ClientHandlerInterface. Each method is called
// when the corresponding command's confirmation/response arrives from the
// slave.
type handler struct{}

func (handler) InterrogationHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("interrogation response: %s", a.Identifier)
	return nil
}

func (handler) CounterInterrogationHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("counter interrogation response: %s", a.Identifier)
	return nil
}

func (handler) ReadHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("read response: %s", a.Identifier)
	return nil
}

func (handler) TestCommandHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("test command response: %s", a.Identifier)
	return nil
}

func (handler) ClockSyncHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("clock sync response: %s", a.Identifier)
	return nil
}

func (handler) ResetProcessHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("reset process response: %s", a.Identifier)
	return nil
}

func (handler) DelayAcquisitionHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("delay acquisition response: %s", a.Identifier)
	return nil
}

// ASDUHandler is called for any type identification not covered above, most
// notably spontaneous process data (M_SP_NA_1, M_ME_NC_1, ...) sent
// unsolicited by the slave.
func (handler) ASDUHandler(_ asdu.Connect, a *asdu.ASDU) error {
	log.Printf("received: %s", a.Identifier)
	return nil
}
