package hislip

import "fmt"

// Instrument is the interface implemented by all virtual instruments.
// Embed InstrumentBase and override SetupCommands and OnInputChanged.
type Instrument interface {
	// SetupCommands registers SCPI handlers on the provided engine.
	// Called once during Start().
	SetupCommands()
	// OnInputChanged is called when a connected output port emits a signal.
	OnInputChanged(portName string, signal Signal)
	// start is the internal lifecycle method driven by InstrumentBase.
	start()
}

// InstrumentBase is the base struct for all virtual instruments.
// Embed it in your instrument struct and call Init before Start.
//
//	type VirtualDMM struct {
//	    hislip.InstrumentBase
//	}
//
//	func (d *VirtualDMM) SetupCommands() {
//	    d.Engine.RegisterHandler("READ?", func(_ *hislip.SCPICommand) (string, bool) {
//	        return "3.14159", true
//	    })
//	}
type InstrumentBase struct {
	Engine    *CommandEngine
	Inputs    map[string]*InputPort
	Outputs   map[string]*OutputPort
	protocols []Protocol
	started   bool
	self      Instrument // back-pointer set by Init
}

// Init configures the instrument. Must be called before Start.
// The self parameter must be a pointer to the embedding struct (for callbacks).
func (b *InstrumentBase) Init(
	self Instrument,
	manufacturer, model, serial, firmware string,
	ieee488 bool,
	commandSeparator string,
	protocols ...Protocol,
) {
	b.self = self
	b.Engine = NewCommandEngine(manufacturer, model, serial, firmware, ieee488, commandSeparator)
	b.Inputs = make(map[string]*InputPort)
	b.Outputs = make(map[string]*OutputPort)
	for _, p := range protocols {
		b.AddProtocol(p)
	}
}

// AddProtocol attaches a transport protocol to this instrument.
func (b *InstrumentBase) AddProtocol(p Protocol) {
	p.Attach(b.Engine)
	b.protocols = append(b.protocols, p)
}

// Protocols returns a copy of the attached protocol list.
func (b *InstrumentBase) Protocols() []Protocol {
	result := make([]Protocol, len(b.protocols))
	copy(result, b.protocols)
	return result
}

// ResourceStrings returns the VISA resource strings for all attached protocols.
func (b *InstrumentBase) ResourceStrings() []string {
	ss := make([]string, len(b.protocols))
	for i, p := range b.protocols {
		ss[i] = p.ResourceString()
	}
	return ss
}

// AddInput adds a named input port.
func (b *InstrumentBase) AddInput(name string) *InputPort {
	port := &InputPort{
		Name: name,
		onChange: func(portName string, signal Signal) {
			if b.self != nil {
				b.self.OnInputChanged(portName, signal)
			}
		},
	}
	b.Inputs[name] = port
	return port
}

// AddOutput adds a named output port.
func (b *InstrumentBase) AddOutput(name string) *OutputPort {
	port := &OutputPort{Name: name}
	b.Outputs[name] = port
	return port
}

// Connect wires an output port on this instrument to an input port on another.
func (b *InstrumentBase) Connect(outputName string, target *InstrumentBase, inputName string) error {
	out, ok := b.Outputs[outputName]
	if !ok {
		return fmt.Errorf("no output port %q (available: %v)", outputName, portNames(b.Outputs))
	}
	in, ok := target.Inputs[inputName]
	if !ok {
		return fmt.Errorf("no input port %q on target (available: %v)", inputName, portNames(target.Inputs))
	}
	out.ConnectTo(in)
	return nil
}

// Start registers commands (once) and starts all protocols.
func (b *InstrumentBase) Start() error {
	if !b.started {
		if b.self != nil {
			b.self.SetupCommands()
		}
		b.started = true
	}
	for _, p := range b.protocols {
		if err := p.Start(); err != nil {
			return err
		}
	}
	return nil
}

// Stop stops all protocol servers.
func (b *InstrumentBase) Stop() {
	for _, p := range b.protocols {
		p.Stop()
	}
}

// SetupCommands is a no-op default. Override in embedding structs.
func (b *InstrumentBase) SetupCommands() {}

// OnInputChanged is a no-op default. Override in embedding structs.
func (b *InstrumentBase) OnInputChanged(_ string, _ Signal) {}

// start satisfies the Instrument interface.
func (b *InstrumentBase) start() { _ = b.Start() }

func portNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	return names
}
