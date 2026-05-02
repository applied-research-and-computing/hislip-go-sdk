// Virtual multi-channel DC power supply.
//
// VISA resource: TCPIP0::127.0.0.1::hislip0::INSTR
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	hislip "github.com/arnc-carbon/hislip-go-sdk"
)

type psuChannel struct {
	voltage      float64
	current      float64
	voltageLimit float64
	currentLimit float64
	enabled      bool
	ovp          float64
}

type VirtualPowerSupply struct {
	hislip.InstrumentBase
	numChannels int
	channels    map[int]*psuChannel
	selected    int
}

func newVirtualPowerSupply(numChannels int) *VirtualPowerSupply {
	chs := make(map[int]*psuChannel, numChannels)
	for i := 1; i <= numChannels; i++ {
		chs[i] = &psuChannel{voltageLimit: 30.0, currentLimit: 5.0, ovp: 33.0}
	}
	return &VirtualPowerSupply{numChannels: numChannels, channels: chs, selected: 1}
}

func (p *VirtualPowerSupply) ch() *psuChannel { return p.channels[p.selected] }

func (p *VirtualPowerSupply) SetupCommands() {
	p.Engine.AddResetHook(p.reset)
	e := p.Engine

	e.RegisterHandler("INST:NSEL", p.handleChannelSelect)
	e.RegisterHandler("INSTRUMENT:NSELECT", p.handleChannelSelect)

	e.RegisterHandler("SOUR:VOLT:PROT", p.handleOVP)
	e.RegisterHandler("SOURCE:VOLTAGE:PROTECTION", p.handleOVP)
	e.RegisterHandler("SOUR:VOLT", p.handleVoltage)
	e.RegisterHandler("SOURCE:VOLTAGE", p.handleVoltage)
	e.RegisterHandler("SOUR:CURR", p.handleCurrent)
	e.RegisterHandler("SOURCE:CURRENT", p.handleCurrent)

	e.RegisterHandler("OUTP:GEN", p.handleOutputGeneral)
	e.RegisterHandler("OUTPUT:GENERAL", p.handleOutputGeneral)
	e.RegisterHandler("OUTP", p.handleOutput)
	e.RegisterHandler("OUTPUT", p.handleOutput)

	e.RegisterHandler("APPL", p.handleApply)

	e.RegisterHandler("MEAS:VOLT", p.handleMeasVoltage)
	e.RegisterHandler("MEASURE:VOLTAGE", p.handleMeasVoltage)
	e.RegisterHandler("MEAS:CURR", p.handleMeasCurrent)
	e.RegisterHandler("MEASURE:CURRENT", p.handleMeasCurrent)
}

func (p *VirtualPowerSupply) OnInputChanged(_ string, _ hislip.Signal) {}

func (p *VirtualPowerSupply) reset() {
	p.selected = 1
	for idx, ch := range p.channels {
		ch.voltage = 0.0
		ch.current = 0.0
		ch.currentLimit = 5.0
		ch.enabled = false
		ch.ovp = 33.0
		p.emitChannel(idx)
	}
}

func (p *VirtualPowerSupply) emitChannel(idx int) {
	ch := p.channels[idx]
	portName := fmt.Sprintf("CH%d", idx)
	if ch.enabled {
		p.Outputs[portName].Emit(hislip.Signal{
			Value: ch.voltage, Unit: "V",
			Metadata: map[string]any{"current_limit": ch.currentLimit},
		})
	} else {
		p.Outputs[portName].Emit(hislip.Signal{Value: 0.0, Unit: "V"})
	}
}

func parseBool(cmd *hislip.SCPICommand) bool {
	v := strings.ToUpper(cmd.ArgString(0, ""))
	return v == "ON" || v == "1"
}

func (p *VirtualPowerSupply) handleChannelSelect(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%d", p.selected), true
	}
	if idx := cmd.ArgInt(0, 0); idx >= 1 && idx <= p.numChannels {
		p.selected = idx
	}
	return "", false
}

func (p *VirtualPowerSupply) handleVoltage(cmd *hislip.SCPICommand) (string, bool) {
	// Longer prefix SOUR:VOLT:PROT wins; this handler only sees plain voltage.
	if cmd.Query {
		return fmt.Sprintf("%.4E", p.ch().voltage), true
	}
	v := cmd.ArgFloat(0, -1)
	if v < 0 {
		return "", false
	}
	if v > p.ch().ovp {
		v = p.ch().ovp
	}
	if v > p.ch().voltageLimit {
		v = p.ch().voltageLimit
	}
	p.ch().voltage = v
	if p.ch().enabled {
		p.emitChannel(p.selected)
	}
	return "", false
}

func (p *VirtualPowerSupply) handleCurrent(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.4E", p.ch().currentLimit), true
	}
	if v := cmd.ArgFloat(0, -1); v >= 0 {
		p.ch().currentLimit = v
	}
	return "", false
}

func (p *VirtualPowerSupply) handleOVP(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.4E", p.ch().ovp), true
	}
	if v := cmd.ArgFloat(0, -1); v >= 0 {
		p.ch().ovp = v
	}
	return "", false
}

func (p *VirtualPowerSupply) handleOutput(cmd *hislip.SCPICommand) (string, bool) {
	// Longer prefix OUTP:GEN wins for general output.
	if cmd.Query {
		if p.ch().enabled {
			return "1", true
		}
		return "0", true
	}
	p.ch().enabled = parseBool(cmd)
	p.emitChannel(p.selected)
	return "", false
}

func (p *VirtualPowerSupply) handleOutputGeneral(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		for _, ch := range p.channels {
			if !ch.enabled {
				return "0", true
			}
		}
		return "1", true
	}
	enabled := parseBool(cmd)
	for idx, ch := range p.channels {
		ch.enabled = enabled
		p.emitChannel(idx)
	}
	return "", false
}

func (p *VirtualPowerSupply) handleApply(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.4E,%.4E", p.ch().voltage, p.ch().currentLimit), true
	}
	// args may be comma-separated within the first arg: "5.0,1.0"
	raw := cmd.ArgString(0, "")
	if raw == "" {
		return "", false
	}
	parts := strings.Split(raw, ",")
	if len(parts) >= 1 {
		var v float64
		fmt.Sscanf(parts[0], "%g", &v)
		p.ch().voltage = v
	}
	if len(parts) >= 2 {
		var v float64
		fmt.Sscanf(parts[1], "%g", &v)
		p.ch().currentLimit = v
	}
	if p.ch().enabled {
		p.emitChannel(p.selected)
	}
	return "", false
}

func (p *VirtualPowerSupply) handleMeasVoltage(_ *hislip.SCPICommand) (string, bool) {
	if p.ch().enabled {
		return fmt.Sprintf("%.6E", p.ch().voltage), true
	}
	return "0.000000E+00", true
}

func (p *VirtualPowerSupply) handleMeasCurrent(_ *hislip.SCPICommand) (string, bool) {
	if p.ch().enabled {
		return fmt.Sprintf("%.6E", p.ch().current), true
	}
	return "0.000000E+00", true
}

func main() {
	proto := hislip.NewHiSLIPProtocol("127.0.0.1", 4880, "hislip0")
	psu := newVirtualPowerSupply(3)
	psu.Init(psu, "CARBON", "PSU6300", "PSU001", "1.0.0", true, ";", proto)
	for i := 1; i <= psu.numChannels; i++ {
		psu.AddOutput(fmt.Sprintf("CH%d", i))
	}

	if err := psu.Start(); err != nil {
		log.Fatalf("Start: %v", err)
	}
	fmt.Printf("Power supply running: %s\n", psu.ResourceStrings()[0])
	fmt.Println("Press Ctrl-C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	psu.Stop()
}
