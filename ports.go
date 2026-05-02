package hislip

// Signal is a value that flows between instrument ports.
//
// Simple use: set Value and Unit.
// Complex use: put additional data in Metadata (frequency, waveform shape,
// impedance, modulation, etc.). Metadata keys follow the conventions used
// in the example instruments (e.g. "frequency", "offset", "function").
type Signal struct {
	Value    float64
	Unit     string
	Metadata map[string]any
}

// OutputPort is an output on a virtual instrument that drives connected inputs.
type OutputPort struct {
	Name        string
	Signal      Signal
	connections []*InputPort
}

// Emit pushes a new signal value to all connected InputPorts.
func (o *OutputPort) Emit(s Signal) {
	o.Signal = s
	for _, p := range o.connections {
		p.receive(s)
	}
}

// ConnectTo wires this output to an InputPort on another instrument.
// The current signal is immediately pushed to the new input so it has
// an initial state.
func (o *OutputPort) ConnectTo(input *InputPort) {
	for _, c := range o.connections {
		if c == input {
			return
		}
	}
	o.connections = append(o.connections, input)
	input.receive(o.Signal)
}

// Disconnect removes the connection to a specific InputPort.
func (o *OutputPort) Disconnect(input *InputPort) {
	kept := o.connections[:0]
	for _, c := range o.connections {
		if c != input {
			kept = append(kept, c)
		}
	}
	o.connections = kept
}

// DisconnectAll removes all connections from this output.
func (o *OutputPort) DisconnectAll() {
	o.connections = nil
}

// Connected reports whether this output has at least one connection.
func (o *OutputPort) Connected() bool {
	return len(o.connections) > 0
}

// InputPort is an input on a virtual instrument that receives signals.
type InputPort struct {
	Name     string
	Signal   Signal
	onChange func(portName string, signal Signal)
}

func (i *InputPort) receive(s Signal) {
	i.Signal = s
	if i.onChange != nil {
		i.onChange(i.Name, s)
	}
}
