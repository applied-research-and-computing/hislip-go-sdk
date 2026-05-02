// Virtual 6.5-digit digital multimeter.
//
// VISA resource: TCPIP0::127.0.0.1::hislip0::INSTR
package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"

	hislip "github.com/arnc-carbon/hislip-go-sdk"
)

type VirtualDMM struct {
	hislip.InstrumentBase
	function      string
	rangeVal      string
	nplc          float64
	impedanceAuto bool
}

func (d *VirtualDMM) SetupCommands() {
	d.Engine.AddResetHook(d.reset)
	e := d.Engine

	e.RegisterHandler("CONF:VOLT", d.configureVoltage)
	e.RegisterHandler("CONFIGURE:VOLTAGE", d.configureVoltage)
	e.RegisterHandler("CONF:CURR", d.configureCurrent)
	e.RegisterHandler("CONFIGURE:CURRENT", d.configureCurrent)
	e.RegisterHandler("CONF:RES", d.configureResistance)
	e.RegisterHandler("CONFIGURE:RESISTANCE", d.configureResistance)
	e.RegisterHandler("CONF:FREQ", d.configureFrequency)
	e.RegisterHandler("CONFIGURE:FREQUENCY", d.configureFrequency)

	e.RegisterHandler("READ?", func(_ *hislip.SCPICommand) (string, bool) {
		return d.reading(), true
	})

	e.RegisterHandler("INP:IMP:AUTO", d.handleImpedanceAuto)
	e.RegisterHandler("INPUT:IMPEDANCE:AUTO", d.handleImpedanceAuto)
	e.RegisterHandler("SENS:VOLT:DC:NPLC", d.handleNPLC)
	e.RegisterHandler("SENSE:VOLTAGE:DC:NPLC", d.handleNPLC)
	e.RegisterHandler("SENS:VOLT:DC:RANG", d.handleRange)
	e.RegisterHandler("SENSE:VOLTAGE:DC:RANGE", d.handleRange)
	e.RegisterHandler("FUNC?", func(_ *hislip.SCPICommand) (string, bool) {
		return d.function, true
	})
	e.RegisterHandler("FUNCTION?", func(_ *hislip.SCPICommand) (string, bool) {
		return d.function, true
	})
}

func (d *VirtualDMM) OnInputChanged(portName string, signal hislip.Signal) {
	if portName != "INPUT" {
		return
	}
	switch {
	case d.function == "VOLT" && (signal.Unit == "V" || signal.Unit == "Vpp"):
		d.Engine.SetProperty("VOLT", fmt.Sprintf("%.6E", signal.Value))
	case d.function == "CURR" && signal.Unit == "A":
		d.Engine.SetProperty("CURR", fmt.Sprintf("%.6E", signal.Value))
	case d.function == "RES":
		d.Engine.SetProperty("RES", fmt.Sprintf("%.6E", signal.Value))
	case d.function == "FREQ":
		freq := signal.Value
		if f, ok := signal.Metadata["frequency"]; ok {
			if fv, ok := f.(float64); ok {
				freq = fv
			}
		}
		d.Engine.SetProperty("FREQ", fmt.Sprintf("%.6E", freq))
	}
}

func (d *VirtualDMM) reset() {
	d.function = "VOLT"
	d.rangeVal = "AUTO"
	d.nplc = 10.0
	d.impedanceAuto = true
}

func (d *VirtualDMM) reading() string {
	v, ok := d.Engine.GetProperty(d.function)
	if !ok {
		return "9.90000E+37"
	}
	var val float64
	if _, err := fmt.Sscanf(v, "%g", &val); err != nil {
		return v
	}
	noise := rand.NormFloat64() * val * 1e-6
	return fmt.Sprintf("%.6E", val+noise)
}

func (d *VirtualDMM) configureVoltage(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return "VOLT " + d.rangeVal, true
	}
	d.function = "VOLT"
	if r := cmd.ArgString(0, ""); r != "" {
		d.rangeVal = strings.ToUpper(r)
	}
	return "", false
}

func (d *VirtualDMM) configureCurrent(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return "CURR " + d.rangeVal, true
	}
	d.function = "CURR"
	if r := cmd.ArgString(0, ""); r != "" {
		d.rangeVal = strings.ToUpper(r)
	}
	return "", false
}

func (d *VirtualDMM) configureResistance(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return "RES " + d.rangeVal, true
	}
	d.function = "RES"
	if r := cmd.ArgString(0, ""); r != "" {
		d.rangeVal = strings.ToUpper(r)
	}
	return "", false
}

func (d *VirtualDMM) configureFrequency(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return "FREQ " + d.rangeVal, true
	}
	d.function = "FREQ"
	if r := cmd.ArgString(0, ""); r != "" {
		d.rangeVal = strings.ToUpper(r)
	}
	return "", false
}

func (d *VirtualDMM) handleImpedanceAuto(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		if d.impedanceAuto {
			return "1", true
		}
		return "0", true
	}
	v := strings.ToUpper(cmd.ArgString(0, ""))
	d.impedanceAuto = v == "ON" || v == "1"
	return "", false
}

func (d *VirtualDMM) handleNPLC(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return fmt.Sprintf("%g", d.nplc), true
	}
	if v := cmd.ArgFloat(0, -1); v >= 0 {
		d.nplc = v
	}
	return "", false
}

func (d *VirtualDMM) handleRange(cmd *hislip.SCPICommand) (string, bool) {
	if cmd.Query {
		return d.rangeVal, true
	}
	if r := cmd.ArgString(0, ""); r != "" {
		d.rangeVal = strings.ToUpper(r)
	}
	return "", false
}

func main() {
	proto := hislip.NewHiSLIPProtocol("127.0.0.1", 4880, "hislip0")
	dmm := &VirtualDMM{
		function:      "VOLT",
		rangeVal:      "AUTO",
		nplc:          10.0,
		impedanceAuto: true,
	}
	dmm.Init(dmm, "CARBON", "DMM6500", "DMM001", "1.0.0", true, ";", proto)
	dmm.AddInput("INPUT")
	dmm.AddInput("TRIGGER_IN")
	dmm.AddOutput("TRIGGER_OUT")

	if err := dmm.Start(); err != nil {
		log.Fatalf("Start: %v", err)
	}
	fmt.Printf("DMM running: %s\n", dmm.ResourceStrings()[0])
	fmt.Println("Press Ctrl-C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	dmm.Stop()
}
