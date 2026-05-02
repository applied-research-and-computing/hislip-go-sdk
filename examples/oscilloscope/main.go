// Virtual 4-channel digital oscilloscope.
//
// VISA resource: TCPIP0::127.0.0.1::hislip0::INSTR
package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"

	hislip "github.com/arnc-carbon/hislip-go-sdk"
)

type channelState struct {
	enabled        bool
	scale          float64
	offset         float64
	coupling       string
	bandwidthLimit bool
	signal         hislip.Signal
}

type VirtualOscilloscope struct {
	hislip.InstrumentBase
	numChannels       int
	channels          map[int]*channelState
	timebaseScale     float64
	timebasePosition  float64
	triggerSource     string
	triggerLevel      float64
	triggerSlope      string
	acquireType       string
	acquireCount      int
	running           bool
	measSource        int
	waveformSource    int
}

func newVirtualOscilloscope(numChannels int) *VirtualOscilloscope {
	chs := make(map[int]*channelState, numChannels)
	for i := 1; i <= numChannels; i++ {
		chs[i] = &channelState{enabled: true, scale: 1.0, coupling: "DC"}
	}
	return &VirtualOscilloscope{
		numChannels:      numChannels,
		channels:         chs,
		timebaseScale:    1e-3,
		triggerSource:    "CH1",
		triggerSlope:     "POS",
		acquireType:      "NORMAL",
		acquireCount:     1,
		running:          true,
		measSource:       1,
		waveformSource:   1,
	}
}

func (o *VirtualOscilloscope) SetupCommands() {
	o.Engine.AddResetHook(o.reset)
	e := o.Engine

	e.RegisterHandler("CHAN", o.handleChannel)
	e.RegisterHandler("CHANNEL", o.handleChannel)

	e.RegisterHandler("TIM:SCAL", o.handleTimebaseScale)
	e.RegisterHandler("TIMEBASE:SCALE", o.handleTimebaseScale)
	e.RegisterHandler("TIM:POS", o.handleTimebasePosition)
	e.RegisterHandler("TIMEBASE:POSITION", o.handleTimebasePosition)

	e.RegisterHandler("TRIG:SOUR", o.handleTriggerSource)
	e.RegisterHandler("TRIGGER:SOURCE", o.handleTriggerSource)
	e.RegisterHandler("TRIG:LEV", o.handleTriggerLevel)
	e.RegisterHandler("TRIGGER:LEVEL", o.handleTriggerLevel)
	e.RegisterHandler("TRIG:SLOP", o.handleTriggerSlope)
	e.RegisterHandler("TRIGGER:SLOPE", o.handleTriggerSlope)

	e.RegisterHandler("MEAS:SOUR", o.handleMeasSource)
	e.RegisterHandler("MEASURE:SOURCE", o.handleMeasSource)
	e.RegisterHandler("MEAS:FREQ", o.handleMeasFreq)
	e.RegisterHandler("MEASURE:FREQUENCY", o.handleMeasFreq)
	e.RegisterHandler("MEAS:VPP", o.handleMeasVPP)
	e.RegisterHandler("MEASURE:VPP", o.handleMeasVPP)
	e.RegisterHandler("MEAS:VMAX", o.handleMeasVMAX)
	e.RegisterHandler("MEASURE:VMAX", o.handleMeasVMAX)
	e.RegisterHandler("MEAS:VMIN", o.handleMeasVMIN)
	e.RegisterHandler("MEASURE:VMIN", o.handleMeasVMIN)
	e.RegisterHandler("MEAS:VRMS", o.handleMeasVRMS)
	e.RegisterHandler("MEASURE:VRMS", o.handleMeasVRMS)

	e.RegisterHandler("ACQ:TYPE", o.handleAcquireType)
	e.RegisterHandler("ACQUIRE:TYPE", o.handleAcquireType)
	e.RegisterHandler("ACQ:COUN", o.handleAcquireCount)
	e.RegisterHandler("ACQUIRE:COUNT", o.handleAcquireCount)

	e.RegisterHandler("RUN", func(_ *hislip.SCPICommand) (string, bool) { o.running = true; return "", false })
	e.RegisterHandler("STOP", func(_ *hislip.SCPICommand) (string, bool) { o.running = false; return "", false })
	e.RegisterHandler("SING", func(_ *hislip.SCPICommand) (string, bool) { o.running = false; return "", false })
	e.RegisterHandler("SINGLE", func(_ *hislip.SCPICommand) (string, bool) { o.running = false; return "", false })

	e.RegisterHandler("WAV:SOUR", o.handleWaveformSource)
	e.RegisterHandler("WAVEFORM:SOURCE", o.handleWaveformSource)
	e.RegisterHandler("WAV:DATA", o.handleWaveformData)
	e.RegisterHandler("WAVEFORM:DATA", o.handleWaveformData)
}

func (o *VirtualOscilloscope) OnInputChanged(portName string, sig hislip.Signal) {
	if !strings.HasPrefix(portName, "CH") {
		return
	}
	var idx int
	if _, err := fmt.Sscanf(portName[2:], "%d", &idx); err == nil {
		if ch, ok := o.channels[idx]; ok {
			ch.signal = sig
		}
	}
}

func (o *VirtualOscilloscope) reset() {
	for _, ch := range o.channels {
		ch.enabled = true
		ch.scale = 1.0
		ch.offset = 0.0
		ch.coupling = "DC"
		ch.bandwidthLimit = false
		ch.signal = hislip.Signal{}
	}
	o.timebaseScale = 1e-3
	o.timebasePosition = 0.0
	o.triggerSource = "CH1"
	o.triggerLevel = 0.0
	o.triggerSlope = "POS"
	o.acquireType = "NORMAL"
	o.acquireCount = 1
	o.running = true
	o.measSource = 1
	o.waveformSource = 1
}

// parseChannelCmd extracts (channelNum, subcmd, value) from commands like "CHAN1:SCAL 0.5".
func (o *VirtualOscilloscope) parseChannelCmd(raw string) (int, string, string) {
	upper := strings.ToUpper(raw)
	var prefix string
	if strings.HasPrefix(upper, "CHANNEL") {
		prefix = "CHANNEL"
	} else if strings.HasPrefix(upper, "CHAN") {
		prefix = "CHAN"
	} else {
		return 0, "", ""
	}
	rest := upper[len(prefix):]
	digits := ""
	for _, c := range rest {
		if c >= '0' && c <= '9' {
			digits += string(c)
		} else {
			break
		}
	}
	if digits == "" {
		return 0, "", ""
	}
	var idx int
	fmt.Sscanf(digits, "%d", &idx)

	colonPos := len(prefix) + len(digits)
	if colonPos >= len(upper) || upper[colonPos] != ':' {
		return idx, "", ""
	}
	subcmdRaw := raw[colonPos+1:]
	parts := strings.SplitN(subcmdRaw, " ", 2)
	subcmd := strings.ToUpper(parts[0])
	value := ""
	if len(parts) > 1 {
		value = strings.TrimSpace(parts[1])
	}
	return idx, subcmd, value
}

func (o *VirtualOscilloscope) handleChannel(cmd *hislip.SCPICommand) (string, bool) {
	idx, subcmd, value := o.parseChannelCmd(cmd.Raw)
	ch, ok := o.channels[idx]
	if !ok {
		return "", false
	}

	isQuery := strings.HasSuffix(strings.TrimRight(subcmd, " "), "?") || cmd.Query
	subcmd = strings.TrimSuffix(subcmd, "?")

	switch {
	case strings.HasPrefix(subcmd, "DISP") || strings.HasPrefix(subcmd, "DIS"):
		if isQuery {
			if ch.enabled {
				return "1", true
			}
			return "0", true
		}
		ch.enabled = value == "ON" || value == "1"
	case strings.HasPrefix(subcmd, "SCAL"):
		if isQuery {
			return fmt.Sprintf("%.4E", ch.scale), true
		}
		var v float64
		if _, err := fmt.Sscanf(value, "%g", &v); err == nil {
			ch.scale = v
		}
	case strings.HasPrefix(subcmd, "OFFS"):
		if isQuery {
			return fmt.Sprintf("%.4E", ch.offset), true
		}
		var v float64
		if _, err := fmt.Sscanf(value, "%g", &v); err == nil {
			ch.offset = v
		}
	case strings.HasPrefix(subcmd, "COUP"):
		if isQuery {
			return ch.coupling, true
		}
		if value == "DC" || value == "AC" || value == "GND" {
			ch.coupling = value
		}
	case strings.HasPrefix(subcmd, "BWL"):
		if isQuery {
			if ch.bandwidthLimit {
				return "1", true
			}
			return "0", true
		}
		ch.bandwidthLimit = value == "ON" || value == "1"
	}
	return "", false
}

func (o *VirtualOscilloscope) handleTimebaseScale(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.4E", o.timebaseScale), true
	}
	if v := cmd.ArgFloat(0, -1); v > 0 {
		o.timebaseScale = v
	}
	return "", false
}

func (o *VirtualOscilloscope) handleTimebasePosition(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.4E", o.timebasePosition), true
	}
	o.timebasePosition = cmd.ArgFloat(0, o.timebasePosition)
	return "", false
}

func (o *VirtualOscilloscope) handleTriggerSource(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return o.triggerSource, true
	}
	if v := cmd.ArgString(0, ""); v != "" {
		o.triggerSource = strings.ToUpper(v)
	}
	return "", false
}

func (o *VirtualOscilloscope) handleTriggerLevel(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%.4E", o.triggerLevel), true
	}
	o.triggerLevel = cmd.ArgFloat(0, o.triggerLevel)
	return "", false
}

func (o *VirtualOscilloscope) handleTriggerSlope(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return o.triggerSlope, true
	}
	if v := strings.ToUpper(cmd.ArgString(0, "")); v == "POS" || v == "NEG" || v == "EITH" {
		o.triggerSlope = v
	}
	return "", false
}

func (o *VirtualOscilloscope) handleMeasSource(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("CH%d", o.measSource), true
	}
	src := strings.ToUpper(cmd.ArgString(0, ""))
	if strings.HasPrefix(src, "CH") {
		var idx int
		if _, err := fmt.Sscanf(src[2:], "%d", &idx); err == nil {
			if _, ok := o.channels[idx]; ok {
				o.measSource = idx
			}
		}
	}
	return "", false
}

func (o *VirtualOscilloscope) measSignal() hislip.Signal {
	ch := o.channels[o.measSource]
	if ch.coupling == "GND" {
		return hislip.Signal{Value: 0.0, Unit: "V"}
	}
	return ch.signal
}

func isDC(sig hislip.Signal) bool {
	_, hasFreq := sig.Metadata["frequency"]
	return sig.Unit == "V" && !hasFreq
}

func (o *VirtualOscilloscope) handleMeasFreq(_ *hislip.SCPICommand) (string, bool) {
	sig := o.measSignal()
	freq := 0.0
	if f, ok := sig.Metadata["frequency"]; ok {
		if fv, ok := f.(float64); ok {
			freq = fv
		}
	}
	return fmt.Sprintf("%.6E", freq), true
}

func (o *VirtualOscilloscope) handleMeasVPP(_ *hislip.SCPICommand) (string, bool) {
	sig := o.measSignal()
	if isDC(sig) {
		return fmt.Sprintf("%.6E", 0.0), true
	}
	return fmt.Sprintf("%.6E", sig.Value), true
}

func (o *VirtualOscilloscope) handleMeasVMAX(_ *hislip.SCPICommand) (string, bool) {
	sig := o.measSignal()
	off := 0.0
	if v, ok := sig.Metadata["offset"]; ok {
		if fv, ok := v.(float64); ok {
			off = fv
		}
	}
	if isDC(sig) {
		return fmt.Sprintf("%.6E", sig.Value), true
	}
	return fmt.Sprintf("%.6E", sig.Value/2.0+off), true
}

func (o *VirtualOscilloscope) handleMeasVMIN(_ *hislip.SCPICommand) (string, bool) {
	sig := o.measSignal()
	off := 0.0
	if v, ok := sig.Metadata["offset"]; ok {
		if fv, ok := v.(float64); ok {
			off = fv
		}
	}
	if isDC(sig) {
		return fmt.Sprintf("%.6E", sig.Value), true
	}
	return fmt.Sprintf("%.6E", -sig.Value/2.0+off), true
}

func (o *VirtualOscilloscope) handleMeasVRMS(_ *hislip.SCPICommand) (string, bool) {
	sig := o.measSignal()
	if isDC(sig) {
		return fmt.Sprintf("%.6E", sig.Value), true
	}
	vrms := sig.Value / (2.0 * math.Sqrt2)
	return fmt.Sprintf("%.6E", vrms), true
}

func (o *VirtualOscilloscope) handleAcquireType(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return o.acquireType, true
	}
	v := strings.ToUpper(cmd.ArgString(0, ""))
	valid := map[string]bool{"NORMAL": true, "NORM": true, "AVERAGE": true, "AVER": true, "PEAK": true, "HRESOLUTION": true, "HRES": true}
	if valid[v] {
		o.acquireType = v
	}
	return "", false
}

func (o *VirtualOscilloscope) handleAcquireCount(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%d", o.acquireCount), true
	}
	if v := cmd.ArgInt(0, -1); v > 0 {
		o.acquireCount = v
	}
	return "", false
}

func (o *VirtualOscilloscope) handleWaveformSource(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("CH%d", o.waveformSource), true
	}
	src := strings.ToUpper(cmd.ArgString(0, ""))
	if strings.HasPrefix(src, "CH") {
		var idx int
		if _, err := fmt.Sscanf(src[2:], "%d", &idx); err == nil {
			if _, ok := o.channels[idx]; ok {
				o.waveformSource = idx
			}
		}
	}
	return "", false
}

func (o *VirtualOscilloscope) handleWaveformData(_ *hislip.SCPICommand) (string, bool) {
	sig := o.channels[o.waveformSource].signal
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

	numPoints := int(1e-3 / o.timebaseScale * 1000)
	if numPoints < 10 {
		numPoints = 10
	}
	if numPoints > 10000 {
		numPoints = 10000
	}
	window := 10.0 * o.timebaseScale

	points := make([]string, numPoints)
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
			frac := math.Mod(phase/(2.0*math.Pi), 1.0)
			y = amplitude * (frac - 0.5)
		case "DC":
			y = 0.0
		default:
			y = amplitude / 2.0 * math.Sin(phase)
		}
		points[i] = fmt.Sprintf("%.4E", y+offset)
	}
	return strings.Join(points, ","), true
}

func main() {
	proto := hislip.NewHiSLIPProtocol("127.0.0.1", 4880, "hislip0")
	osc := newVirtualOscilloscope(4)
	osc.Init(osc, "CARBON", "DSO4000", "DSO001", "1.0.0", true, ";", proto)
	for i := 1; i <= osc.numChannels; i++ {
		osc.AddInput(fmt.Sprintf("CH%d", i))
	}
	osc.AddInput("EXT_TRIG")
	osc.AddOutput("TRIGGER_OUT")

	if err := osc.Start(); err != nil {
		log.Fatalf("Start: %v", err)
	}
	fmt.Printf("Oscilloscope running: %s\n", osc.ResourceStrings()[0])
	fmt.Println("Press Ctrl-C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	osc.Stop()
}
