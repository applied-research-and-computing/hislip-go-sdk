// Package hislip implements a Go SDK for building virtual test instruments
// that speak HiSLIP (IVI-6.1). Create simulated SCPI instruments accessible
// from any VISA client — PyVISA, NI MAX, Keysight Connection Expert, or the
// Carbon platform.
//
// Quick start:
//
//	type VirtualDMM struct{ hislip.InstrumentBase }
//
//	func (d *VirtualDMM) SetupCommands() {
//	    d.Engine.RegisterHandler("READ?", func(_ *hislip.SCPICommand) (string, bool) {
//	        return "3.14159", true
//	    })
//	}
//
//	proto := hislip.NewHiSLIPProtocol("127.0.0.1", 4880, "hislip0")
//	dmm := &VirtualDMM{}
//	dmm.Init("ACME", "DMM100", "SN001", "1.0.0", true, ";", proto)
//	dmm.Start()
//	// VISA resource: TCPIP0::127.0.0.1::hislip0::INSTR
package hislip
