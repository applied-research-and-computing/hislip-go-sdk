// Virtual signal/function generator.
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

type VirtualSignalGenerator struct {
	hislip.InstrumentBase
	frequency     float64
	amplitude     float64
	offset        float64
	function      string
	outputEnabled bool
	burstState    bool
	burstNCycles  int
	sweepState    bool
}

func (s *VirtualSignalGenerator) SetupCommands() {
	s.Engine.AddResetHook(s.reset)
	e := s.Engine

	e.RegisterHandler("SOUR:FREQ", s.handleFrequency)
	e.RegisterHandler("SOURCE:FREQUENCY", s.handleFrequency)
	e.RegisterHandler("FREQ", s.handleFrequency)

	e.RegisterHandler("SOUR:VOLT:LEV", s.handleAmplitude)
	e.RegisterHandler("SOURCE:VOLTAGE:LEVEL", s.handleAmplitude)
	e.RegisterHandler("SOUR:VOLT:OFFS", s.handleOffset)
	e.RegisterHandler("SOURCE:VOLTAGE:OFFSET", s.handleOffset)
	e.RegisterHandler("SOUR:VOLT", s.handleAmplitude)
	e.RegisterHandler("SOURCE:VOLTAGE", s.handleAmplitude)
	e.RegisterHandler("VOLT", s.handleAmplitude)

	e.RegisterHandler("SOUR:FUNC", s.handleFunction)
	e.RegisterHandler("SOURCE:FUNCTION", s.handleFunction)
	e.RegisterHandler("FUNC", s.handleFunction)

	e.RegisterHandler("OUTP:STAT", s.handleOutput)
	e.RegisterHandler("OUTPUT:STATE", s.handleOutput)
	e.RegisterHandler("OUTP", s.handleOutput)
	e.RegisterHandler("OUTPUT", s.handleOutput)

	e.RegisterHandler("SOUR:BURS:STAT", s.handleBurstState)
	e.RegisterHandler("SOURCE:BURST:STATE", s.handleBurstState)
	e.RegisterHandler("SOUR:BURS:NCYC", s.handleBurstNCycles)
	e.RegisterHandler("SOURCE:BURST:NCYCLES", s.handleBurstNCycles)

	e.RegisterHandler("SOUR:SWE:STAT", s.handleSweepState)
	e.RegisterHandler("SOURCE:SWEEP:STATE", s.handleSweepState)
}

func (s *VirtualSignalGenerator) OnInputChanged(_ string, _ hislip.Signal) {}

func (s *VirtualSignalGenerator) reset() {
	s.frequency = 1000.0
	s.amplitude = 1.0
	s.offset = 0.0
	s.function = "SIN"
	s.outputEnabled = false
	s.burstState = false
	s.burstNCycles = 1
	s.sweepState = false
}

func (s *VirtualSignalGenerator) emitOutput() {
	if s.outputEnabled {
		s.Outputs["OUTPUT"].Emit(hislip.Signal{
			Value: s.amplitude,
			Unit:  "Vpp",
			Metadata: map[string]any{
				"frequency": s.frequency,
				"offset":    s.offset,
				"function":  s.function,
			},
		})
	}
}

func (s *VirtualSignalGenerator) handleFrequency(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.6E", s.frequency), true
	}
	if v := cmd.ArgFloat(0, -1); v >= 0 {
		s.frequency = v
		s.emitOutput()
	}
	return "", false
}

func (s *VirtualSignalGenerator) handleAmplitude(cmd *hislip.SCPICommand) (string, bool) {
	// Don't handle OFFS here — let the longer prefix "SOUR:VOLT:OFFS" win
	if strings.Contains(strings.ToUpper(cmd.Raw), "OFFS") {
		return s.handleOffset(cmd)
	}
	if cmd.Query {
		return fmt.Sprintf("%.4E", s.amplitude), true
	}
	if v := cmd.ArgFloat(0, -1); v >= 0 {
		s.amplitude = v
		s.emitOutput()
	}
	return "", false
}

func (s *VirtualSignalGenerator) handleOffset(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.4E", s.offset), true
	}
	s.offset = cmd.ArgFloat(0, s.offset)
	s.emitOutput()
	return "", false
}

func (s *VirtualSignalGenerator) handleFunction(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return s.function, true
	}
	f := strings.ToUpper(cmd.ArgString(0, ""))
	valid := map[string]bool{"SIN": true, "SQU": true, "RAMP": true, "PULS": true, "NOIS": true, "DC": true}
	if valid[f] {
		s.function = f
		s.emitOutput()
	}
	return "", false
}

func parseBool(cmd *hislip.SCPICommand) bool {
	v := strings.ToUpper(cmd.ArgString(0, ""))
	return v == "ON" || v == "1"
}

func (s *VirtualSignalGenerator) handleOutput(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		if s.outputEnabled {
			return "1", true
		}
		return "0", true
	}
	s.outputEnabled = parseBool(cmd)
	if s.outputEnabled {
		s.emitOutput()
	}
	return "", false
}

func (s *VirtualSignalGenerator) handleBurstState(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		if s.burstState {
			return "1", true
		}
		return "0", true
	}
	s.burstState = parseBool(cmd)
	return "", false
}

func (s *VirtualSignalGenerator) handleBurstNCycles(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%d", s.burstNCycles), true
	}
	if v := cmd.ArgInt(0, -1); v >= 0 {
		s.burstNCycles = v
	}
	return "", false
}

func (s *VirtualSignalGenerator) handleSweepState(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		if s.sweepState {
			return "1", true
		}
		return "0", true
	}
	s.sweepState = parseBool(cmd)
	return "", false
}

func main() {
	proto := hislip.NewHiSLIPProtocol("127.0.0.1", 4880, "hislip0")
	sg := &VirtualSignalGenerator{
		frequency:    1000.0,
		amplitude:    1.0,
		offset:       0.0,
		function:     "SIN",
		burstNCycles: 1,
	}
	sg.Init(sg, "CARBON", "SG3300", "SG001", "1.0.0", true, ";", proto)
	sg.AddOutput("OUTPUT")
	sg.AddInput("TRIGGER_IN")
	sg.AddOutput("SYNC")

	if err := sg.Start(); err != nil {
		log.Fatalf("Start: %v", err)
	}
	fmt.Printf("Signal generator running: %s\n", sg.ResourceStrings()[0])
	fmt.Println("Press Ctrl-C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	sg.Stop()
}
