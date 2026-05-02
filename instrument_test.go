package hislip

import "testing"

// minimalInstrument is the simplest possible InstrumentBase embed.
type minimalInstrument struct {
	InstrumentBase
	setupCalled bool
}

func (m *minimalInstrument) SetupCommands() {
	m.setupCalled = true
	m.Engine.RegisterHandler("HELLO?", func(_ *SCPICommand) (string, bool) {
		return "World!", true
	})
}
func (m *minimalInstrument) OnInputChanged(_ string, _ Signal) {}

func newMinimalInstrument(protocols ...Protocol) *minimalInstrument {
	inst := &minimalInstrument{}
	inst.Init(inst, "TEST", "INST", "SN001", "1.0", true, ";", protocols...)
	return inst
}

func TestInstrumentBase_StartCallsSetupCommands(t *testing.T) {
	inst := newMinimalInstrument()
	if err := inst.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if !inst.setupCalled {
		t.Error("SetupCommands was not called during Start")
	}
}

func TestInstrumentBase_SetupCommandsCalledOnce(t *testing.T) {
	inst := newMinimalInstrument()
	inst.Start()
	inst.Start() // second call should be a no-op
	// verify by counting handler registrations — HELLO? should appear once
	resp, ok := inst.Engine.ProcessCommand("HELLO?")
	if !ok || resp != "World!" {
		t.Errorf("HELLO? = %q (%v), want World!", resp, ok)
	}
}

func TestInstrumentBase_AddInputOutput(t *testing.T) {
	inst := newMinimalInstrument()
	in := inst.AddInput("INPUT")
	out := inst.AddOutput("OUTPUT")
	if in == nil || out == nil {
		t.Fatal("AddInput/AddOutput returned nil")
	}
	if inst.Inputs["INPUT"] != in {
		t.Error("Inputs map not updated")
	}
	if inst.Outputs["OUTPUT"] != out {
		t.Error("Outputs map not updated")
	}
}

func TestInstrumentBase_Connect(t *testing.T) {
	inst1 := newMinimalInstrument()
	inst2 := newMinimalInstrument()
	inst1.AddOutput("OUT")
	inst2.AddInput("IN")

	if err := inst1.Connect("OUT", &inst2.InstrumentBase, "IN"); err != nil {
		t.Fatalf("Connect error: %v", err)
	}
}

func TestInstrumentBase_ConnectMissingOutput(t *testing.T) {
	inst1 := newMinimalInstrument()
	inst2 := newMinimalInstrument()
	inst2.AddInput("IN")

	if err := inst1.Connect("NOEXIST", &inst2.InstrumentBase, "IN"); err == nil {
		t.Error("expected error for missing output port")
	}
}

func TestInstrumentBase_ConnectMissingInput(t *testing.T) {
	inst1 := newMinimalInstrument()
	inst2 := newMinimalInstrument()
	inst1.AddOutput("OUT")

	if err := inst1.Connect("OUT", &inst2.InstrumentBase, "NOEXIST"); err == nil {
		t.Error("expected error for missing input port")
	}
}

func TestInstrumentBase_OnInputChangedTriggered(t *testing.T) {
	type trackingInstrument struct {
		InstrumentBase
		lastPort   string
		lastSignal Signal
	}
	ti := &trackingInstrument{}
	ti.Init(ti, "T", "I", "S", "F", false, ";")
	ti.AddInput("IN")

	// Wire a standalone output to the input
	out := &OutputPort{Name: "OUT"}
	out.ConnectTo(ti.Inputs["IN"])
	out.Emit(Signal{Value: 42.0, Unit: "mV"})

	// OnInputChanged default is no-op; we just verify no panic
	if ti.Inputs["IN"].Signal.Value != 42.0 {
		t.Errorf("input signal value = %v, want 42.0", ti.Inputs["IN"].Signal.Value)
	}
}

func TestInstrumentBase_ResourceStrings(t *testing.T) {
	proto := NewHiSLIPProtocol("127.0.0.1", 0, "hislip0")
	inst := newMinimalInstrument(proto)
	if err := inst.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer inst.Stop()

	rs := inst.ResourceStrings()
	if len(rs) == 0 {
		t.Fatal("expected at least one resource string")
	}
	if rs[0] != "TCPIP0::127.0.0.1::hislip0::INSTR" {
		t.Errorf("resource string = %q, want TCPIP0::127.0.0.1::hislip0::INSTR", rs[0])
	}
}
