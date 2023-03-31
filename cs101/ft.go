// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs101

// Using FT1.2 frame format
const (
	startVarFrame byte = 0x68 // variable length frame start character
	startFixFrame byte = 0x10 // fixed length frame start character
	endFrame      byte = 0x16
)

// Control domain definition
const (

	// Initiator station to slave station specific
	FCV = 1 << 4 // Frame Count Valid Bit
	FCB = 1 << 5 // frame count bit
	// Slave station specific to starting station
	DFC     = 1 << 4 // data flow control bit
	ACD_RES = 1 << 5 // Access Bit Required, Unbalanced ACD, Balance Reserved
	// start message bit:
	// PRM = 0, Transmission of telegrams from the slave station to the initiator station;
	// PRM = 1, Transmission of telegrams from the master station to the slave station
	RPM     = 1 << 6
	RES_DIR = 1 << 7 //Non-equilibrium is preserved, balance is the direction

	// The function code of the control field in the message transmitted from the initiator station to the slave station(PRM = 1)
	FccResetRemoteLink                 = iota // reset remote link
	FccResetUserProcess                       // reset user process
	FccBalanceTestLink                        // Link test function
	FccUserDataWithConfirmed                  // User data, need to confirm
	FccUserDataWithUnconfirmed                // User data, no confirmation required
	_                                         // reserve
	_                                         // Manufacturer and user agreement definition
	_                                         // Manufacturer and user agreement definition
	FccUnbalanceWithRequestBitResponse        // Respond with Access Required Bits
	FccLinkStatus                             // request link state
	FccUnbalanceLevel1UserData                // Request Level 1 User Data
	FccUnbalanceLevel2UserData                // Request Level 2 User Data
	// 12-13: spare
	// 14-15: Manufacturer and user agreement definition

	// The function code of the control field in the message transmitted from the slave station to the initiator station(PRM = 0)
	FcsConfirmed                 = iota // Recognized: affirmatively recognized
	FcsNConfirmed                       // Negative acknowledgment: no message received, link busy
	_                                   // reserve
	_                                   // reserve
	_                                   // reserve
	_                                   // reserve
	_                                   // Manufacturer and user agreement definition
	_                                   // Manufacturer and user agreement definition
	FcsUnbalanceResponse                // User data
	FcsUnbalanceNegativeResponse        // denial brother: No call data
	_                                   // reserve
	FcsStatus                           // link status or access required
	// 12: spare
	// 13: Manufacturer and user agreement definition
	// 14: link service not working
	// 15: Link service not completed
)

// Ft12 ...
type Ft12 struct {
	start        byte
	apduFiledLen byte
	ctrl         byte
	address      uint16
	checksum     byte
	end          byte
}
