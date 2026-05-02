// Virtual bench: oscilloscope + signal generator wired together.
//
// The signal generator OUTPUT is connected to the oscilloscope CH1 input.
// The SG starts with output enabled at 1 kHz / 2 Vpp sine, so the
// oscilloscope immediately has a live waveform on CH1.
//
//	Oscilloscope  HiSLIP  4880   TCPIP0::127.0.0.1::hislip0::INSTR
//	Signal Gen    HiSLIP  4881   TCPIP0::127.0.0.1::hislip0::INSTR
package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"

	hislip "github.com/arnc-carbon/hislip-go-sdk"
)

// ---------------------------------------------------------------------------
// Signal generator (inline to keep bench self-contained)
// ---------------------------------------------------------------------------

type SG struct {
	hislip.InstrumentBase
	frequency     float64
	amplitude     float64
	offset        float64
	function      string
	outputEnabled bool
}

func (s *SG) SetupCommands() {
	e := s.Engine
	e.RegisterHandler("SOUR:FREQ", s.handleFreq)
	e.RegisterHandler("FREQ", s.handleFreq)
	e.RegisterHandler("SOUR:VOLT:OFFS", s.handleOffset)
	e.RegisterHandler("SOUR:VOLT", s.handleAmplitude)
	e.RegisterHandler("VOLT", s.handleAmplitude)
	e.RegisterHandler("SOUR:FUNC", s.handleFunc)
	e.RegisterHandler("FUNC", s.handleFunc)
	e.RegisterHandler("OUTP", s.handleOutput)
	e.RegisterHandler("OUTPUT", s.handleOutput)
}
func (s *SG) OnInputChanged(_ string, _ hislip.Signal) {}
func (s *SG) emit() {
	if s.outputEnabled {
		s.Outputs["OUTPUT"].Emit(hislip.Signal{
			Value: s.amplitude, Unit: "Vpp",
			Metadata: map[string]any{"frequency": s.frequency, "offset": s.offset, "function": s.function},
		})
	}
}
func (s *SG) handleFreq(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.6E", s.frequency), true
	}
	if v := cmd.ArgFloat(0, -1); v >= 0 {
		s.frequency = v
		s.emit()
	}
	return "", false
}
func (s *SG) handleAmplitude(cmd *hislip.SCPICommand) (string, bool) {
	if strings.Contains(strings.ToUpper(cmd.Raw), "OFFS") {
		return s.handleOffset(cmd)
	}
	if cmd.Query {
		return fmt.Sprintf("%.4E", s.amplitude), true
	}
	if v := cmd.ArgFloat(0, -1); v >= 0 {
		s.amplitude = v
		s.emit()
	}
	return "", false
}
func (s *SG) handleOffset(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.4E", s.offset), true
	}
	s.offset = cmd.ArgFloat(0, s.offset)
	s.emit()
	return "", false
}
func (s *SG) handleFunc(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return s.function, true
	}
	f := strings.ToUpper(cmd.ArgString(0, ""))
	valid := map[string]bool{"SIN": true, "SQU": true, "RAMP": true, "PULS": true, "NOIS": true, "DC": true}
	if valid[f] {
		s.function = f
		s.emit()
	}
	return "", false
}
func (s *SG) handleOutput(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		if s.outputEnabled {
			return "1", true
		}
		return "0", true
	}
	v := strings.ToUpper(cmd.ArgString(0, ""))
	s.outputEnabled = v == "ON" || v == "1"
	if s.outputEnabled {
		s.emit()
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Oscilloscope (inline to keep bench self-contained)
// ---------------------------------------------------------------------------

type OSC struct {
	hislip.InstrumentBase
	ch1Signal     hislip.Signal
	timebaseScale float64
}

func (o *OSC) SetupCommands() {
	e := o.Engine
	e.RegisterHandler("MEAS:FREQ?", func(_ *hislip.SCPICommand) (string, bool) {
		freq := 0.0
		if f, ok := o.ch1Signal.Metadata["frequency"]; ok {
			if fv, ok := f.(float64); ok {
				freq = fv
			}
		}
		return fmt.Sprintf("%.6E", freq), true
	})
	e.RegisterHandler("MEAS:VPP?", func(_ *hislip.SCPICommand) (string, bool) {
		return fmt.Sprintf("%.6E", o.ch1Signal.Value), true
	})
	e.RegisterHandler("WAV:DATA?", func(_ *hislip.SCPICommand) (string, bool) {
		return o.waveformData(), true
	})
	e.RegisterHandler("TIM:SCAL", func(cmd *hislip.SCPICommand) (string, bool) {
		if cmd.Query {
			return fmt.Sprintf("%.4E", o.timebaseScale), true
		}
		if v := cmd.ArgFloat(0, -1); v > 0 {
			o.timebaseScale = v
		}
		return "", false
	})
}
func (o *OSC) OnInputChanged(portName string, sig hislip.Signal) {
	if portName == "CH1" {
		o.ch1Signal = sig
	}
}
func (o *OSC) waveformData() string {
	sig := o.ch1Signal
	amplitude := sig.Value
	if amplitude == 0 {
		amplitude = 1.0
	}
	frequency := 0.0
	offset := 0.0
	funcType := "SIN"
	if f, ok := sig.Metadata["frequency"]; ok {
		if fv, ok := f.(float64); ok {
			frequency = fv
		}
	}
	if v, ok := sig.Metadata["offset"]; ok {
		if fv, ok := v.(float64); ok {
			offset = fv
		}
	}
	if f, ok := sig.Metadata["function"]; ok {
		if sv, ok := f.(string); ok {
			funcType = sv
		}
	}
	numPoints := 1000
	window := 10.0 * o.timebaseScale
	pts := make([]string, numPoints)
	for i := 0; i < numPoints; i++ {
		t := float64(i) / float64(numPoints) * window
		phase := 2.0 * math.Pi * frequency * t
		if frequency == 0 {
			phase = 2.0 * math.Pi * float64(i) / float64(numPoints)
		}
		var y float64
		switch funcType {
		case "SQU":
			if math.Sin(phase) >= 0 {
				y = amplitude / 2.0
			} else {
				y = -amplitude / 2.0
			}
		case "RAMP":
			y = amplitude * (math.Mod(phase/(2*math.Pi), 1.0) - 0.5)
		default:
			y = amplitude / 2.0 * math.Sin(phase)
		}
		noise := rand.NormFloat64() * 0.001 * amplitude
		pts[i] = fmt.Sprintf("%.4E", y+offset+noise)
	}
	return strings.Join(pts, ",")
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	sgProto := hislip.NewHiSLIPProtocol("127.0.0.1", 4881, "hislip0")
	sg := &SG{frequency: 1000.0, amplitude: 2.0, function: "SIN"}
	sg.Init(sg, "CARBON", "SG3300", "SG001", "1.0.0", true, ";", sgProto)
	sg.AddOutput("OUTPUT")

	oscProto := hislip.NewHiSLIPProtocol("127.0.0.1", 4880, "hislip0")
	osc := &OSC{timebaseScale: 1e-3}
	osc.Init(osc, "CARBON", "DSO4000", "DSO001", "1.0.0", true, ";", oscProto)
	osc.AddInput("CH1")

	// Wire SG OUTPUT → OSC CH1
	if err := sg.Connect("OUTPUT", &osc.InstrumentBase, "CH1"); err != nil {
		log.Fatalf("Connect: %v", err)
	}

	if err := osc.Start(); err != nil {
		log.Fatalf("osc Start: %v", err)
	}
	if err := sg.Start(); err != nil {
		log.Fatalf("sg Start: %v", err)
	}

	// Enable output so CH1 has a live waveform immediately
	sg.Engine.ProcessCommand("SOUR:FREQ 1000")
	sg.Engine.ProcessCommand("SOUR:VOLT 2")
	sg.Engine.ProcessCommand("OUTP ON")

	fmt.Println("Virtual bench running:")
	fmt.Printf("  Oscilloscope  HiSLIP: TCPIP0::127.0.0.1::hislip0::INSTR  (port 4880)\n")
	fmt.Printf("  Signal Gen    HiSLIP: TCPIP0::127.0.0.1::hislip0::INSTR  (port 4881)\n")
	fmt.Println("  Wiring: SG OUTPUT -> OSC CH1  (1 kHz, 2 Vpp sine)")
	fmt.Println("Press Ctrl-C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	sg.Stop()
	osc.Stop()
	fmt.Println("\nStopped.")
}
