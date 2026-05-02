// Minimal HiSLIP instrument in ~20 lines.
//
// VISA resource: TCPIP0::127.0.0.1::hislip0::INSTR
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	hislip "github.com/arnc-carbon/hislip-go-sdk"
)

type HelloInstrument struct{ hislip.InstrumentBase }

func (h *HelloInstrument) SetupCommands() {
	h.Engine.RegisterHandler("HELLO?", func(_ *hislip.SCPICommand) (string, bool) {
		return "World!", true
	})
}
func (h *HelloInstrument) OnInputChanged(_ string, _ hislip.Signal) {}

func main() {
	proto := hislip.NewHiSLIPProtocol("127.0.0.1", 4880, "hislip0")
	inst := &HelloInstrument{}
	inst.Init(inst, "DEMO", "HELLO", "SN001", "1.0.0", true, ";", proto)
	if err := inst.Start(); err != nil {
		log.Fatalf("Start: %v", err)
	}
	fmt.Printf("Running: %s\n", inst.ResourceStrings()[0])
	fmt.Println("Send '*IDN?' or 'HELLO?' via any VISA client. Ctrl-C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	inst.Stop()
}
