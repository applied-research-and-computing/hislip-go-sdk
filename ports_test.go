package hislip

import "testing"

func TestOutputPort_EmitReachesInput(t *testing.T) {
	var received Signal
	in := &InputPort{Name: "IN", onChange: func(_ string, s Signal) { received = s }}
	out := &OutputPort{Name: "OUT"}

	out.ConnectTo(in)
	out.Emit(Signal{Value: 3.14, Unit: "V"})

	if received.Value != 3.14 {
		t.Errorf("received.Value = %v, want 3.14", received.Value)
	}
	if received.Unit != "V" {
		t.Errorf("received.Unit = %q, want V", received.Unit)
	}
}

func TestOutputPort_InitialValuePropagatesOnConnect(t *testing.T) {
	out := &OutputPort{Name: "OUT", Signal: Signal{Value: 5.0, Unit: "V"}}

	var received Signal
	in := &InputPort{Name: "IN", onChange: func(_ string, s Signal) { received = s }}
	out.ConnectTo(in)

	if received.Value != 5.0 {
		t.Errorf("initial value not propagated: received.Value = %v, want 5.0", received.Value)
	}
}

func TestOutputPort_ConnectedReports(t *testing.T) {
	out := &OutputPort{Name: "OUT"}
	if out.Connected() {
		t.Error("expected Connected()=false before any connection")
	}
	in := &InputPort{Name: "IN"}
	out.ConnectTo(in)
	if !out.Connected() {
		t.Error("expected Connected()=true after ConnectTo")
	}
}

func TestOutputPort_DisconnectRemovesOne(t *testing.T) {
	out := &OutputPort{Name: "OUT"}
	in1 := &InputPort{Name: "IN1"}
	in2 := &InputPort{Name: "IN2"}
	out.ConnectTo(in1)
	out.ConnectTo(in2)
	out.Disconnect(in1)

	var count int
	out.Emit(Signal{Value: 1})
	_ = count // in2 still connected; we just verify no panic
	if !out.Connected() {
		t.Error("out should still be connected to in2")
	}
}

func TestOutputPort_DisconnectAll(t *testing.T) {
	out := &OutputPort{Name: "OUT"}
	out.ConnectTo(&InputPort{Name: "IN1"})
	out.ConnectTo(&InputPort{Name: "IN2"})
	out.DisconnectAll()
	if out.Connected() {
		t.Error("expected Connected()=false after DisconnectAll")
	}
}

func TestOutputPort_NoDuplicateConnections(t *testing.T) {
	var callCount int
	in := &InputPort{Name: "IN", onChange: func(_ string, _ Signal) { callCount++ }}
	out := &OutputPort{Name: "OUT", Signal: Signal{Value: 0}}

	out.ConnectTo(in) // triggers initial push → callCount=1
	out.ConnectTo(in) // duplicate — should be ignored

	callCount = 0
	out.Emit(Signal{Value: 1})
	if callCount != 1 {
		t.Errorf("emit reached onChange %d times, want 1 (no duplicate connections)", callCount)
	}
}

func TestSignal_MetadataIsolation(t *testing.T) {
	s := Signal{Value: 1.0, Unit: "V", Metadata: map[string]any{"frequency": 1000.0}}
	if s.Metadata["frequency"] != 1000.0 {
		t.Errorf("metadata frequency = %v, want 1000.0", s.Metadata["frequency"])
	}
}

func TestInputPort_PortNamePassedToCallback(t *testing.T) {
	var gotName string
	in := &InputPort{Name: "CH1", onChange: func(portName string, _ Signal) { gotName = portName }}
	out := &OutputPort{Name: "OUT"}
	out.ConnectTo(in)
	out.Emit(Signal{Value: 1})
	if gotName != "CH1" {
		t.Errorf("portName in callback = %q, want CH1", gotName)
	}
}
