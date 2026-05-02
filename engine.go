package hislip

import (
	"strconv"
	"strings"
	"sync"
)

// Handler processes a SCPI command and optionally returns a response.
// Return (response, true) for queries. Return ("", false) for write commands.
type Handler func(*SCPICommand) (string, bool)

type handlerEntry struct {
	prefix  string // always uppercase
	handler Handler
}

// CommandEngine processes SCPI commands. Thread-safe.
//
// At its core: string in, optional string out. Commands are dispatched to
// registered handlers by longest-prefix matching. A built-in property store
// provides simple get/set semantics.
//
// When ieee488 is true (the default), IEEE 488.2 common commands (*IDN?,
// *RST, *CLS, *STB?, etc.) and basic SCPI commands (SYST:ERR?, MEAS:*)
// are pre-registered.
type CommandEngine struct {
	Manufacturer string
	Model        string
	Serial       string
	Firmware     string

	commandSeparator string

	mu          sync.Mutex
	stb         uint8
	esr         uint8
	ese         uint8
	sre         uint8
	opc         bool
	srqPending  bool
	srqCallback func()
	properties  map[string]string
	resetHooks  []func()

	handlersMu sync.RWMutex
	handlers   []handlerEntry
}

// NewCommandEngine creates a CommandEngine.
// Pass ieee488=true to pre-register IEEE 488.2 and basic SCPI commands.
func NewCommandEngine(manufacturer, model, serial, firmware string, ieee488 bool, commandSeparator string) *CommandEngine {
	if commandSeparator == "" {
		commandSeparator = ";"
	}
	e := &CommandEngine{
		Manufacturer:     manufacturer,
		Model:            model,
		Serial:           serial,
		Firmware:         firmware,
		commandSeparator: commandSeparator,
		properties:       make(map[string]string),
	}
	if ieee488 {
		e.registerIEEE488()
	}
	return e
}

func (e *CommandEngine) registerIEEE488() {
	e.RegisterHandler("*IDN?", e.handleIDN)
	e.RegisterHandler("*RST", e.handleRST)
	e.RegisterHandler("*CLS", e.handleCLS)
	e.RegisterHandler("*STB?", e.handleSTBQuery)
	e.RegisterHandler("*ESR?", e.handleESRQuery)
	e.RegisterHandler("*ESE", e.handleESE)
	e.RegisterHandler("*SRE", e.handleSRE)
	e.RegisterHandler("*OPC?", func(_ *SCPICommand) (string, bool) { return "1", true })
	e.RegisterHandler("*OPC", e.handleOPC)
	e.RegisterHandler("*TST?", func(_ *SCPICommand) (string, bool) { return "0", true })
	e.RegisterHandler("SYST:ERR?", func(_ *SCPICommand) (string, bool) { return `0,"No error"`, true })
	e.RegisterHandler("SYSTEM:ERROR?", func(_ *SCPICommand) (string, bool) { return `0,"No error"`, true })
	e.RegisterHandler("SYST:VERS?", func(_ *SCPICommand) (string, bool) { return "1999.0", true })
	e.RegisterHandler("SYSTEM:VERSION?", func(_ *SCPICommand) (string, bool) { return "1999.0", true })
	e.RegisterHandler("MEAS:", e.handleMeasure)
	e.RegisterHandler("MEASURE:", e.handleMeasure)
	e.setDefaultProperties()
}

func (e *CommandEngine) setDefaultProperties() {
	e.properties["VOLT"] = "1.00000E+00"
	e.properties["CURR"] = "5.00000E-03"
	e.properties["FREQ"] = "1.00000E+03"
	e.properties["RES"]  = "1.00000E+04"
}

// RegisterHandler registers a SCPI command handler.
// When multiple handlers match a command, the longest prefix wins.
// Registration is case-insensitive; prefixes are stored uppercase.
func (e *CommandEngine) RegisterHandler(prefix string, handler Handler) {
	e.handlersMu.Lock()
	defer e.handlersMu.Unlock()
	e.handlers = append(e.handlers, handlerEntry{strings.ToUpper(prefix), handler})
}

// AddResetHook registers a function called after *RST resets engine state.
// Hooks are invoked without holding any engine lock, so they may safely
// call SetProperty and other engine methods.
func (e *CommandEngine) AddResetHook(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.resetHooks = append(e.resetHooks, fn)
}

// SetProperty stores a named property value (key is case-insensitive).
func (e *CommandEngine) SetProperty(key, value string) {
	e.mu.Lock()
	e.properties[strings.ToUpper(key)] = value
	e.mu.Unlock()
}

// GetProperty returns a stored property value.
// The second return value is false if the key has not been set.
func (e *CommandEngine) GetProperty(key string) (string, bool) {
	e.mu.Lock()
	v, ok := e.properties[strings.ToUpper(key)]
	e.mu.Unlock()
	return v, ok
}

// ReadSTB returns the current status byte and clears the SRQ pending flag.
func (e *CommandEngine) ReadSTB() uint8 {
	e.mu.Lock()
	stb := e.getSTBLocked()
	e.srqPending = false
	e.stb &^= 0x40
	e.mu.Unlock()
	return stb
}

// GenerateSRQ asserts bit 6 of the STB and invokes the registered SRQ callback.
func (e *CommandEngine) GenerateSRQ() {
	e.mu.Lock()
	e.srqPending = true
	e.stb |= 0x40
	cb := e.srqCallback
	e.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// OnSRQ registers a callback invoked when GenerateSRQ is called.
// The callback is invoked outside any engine lock.
func (e *CommandEngine) OnSRQ(fn func()) {
	e.mu.Lock()
	e.srqCallback = fn
	e.mu.Unlock()
}

// ProcessCommand processes a command string. The string may contain multiple
// commands separated by the command separator (default ";").
// Returns the joined response string and true if any response was produced.
func (e *CommandEngine) ProcessCommand(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", false
	}

	var cmds []string
	if e.commandSeparator != "" {
		for _, part := range strings.Split(command, e.commandSeparator) {
			if s := strings.TrimSpace(part); s != "" {
				cmds = append(cmds, s)
			}
		}
	} else {
		cmds = []string{command}
	}

	var responses []string
	for _, cmd := range cmds {
		if resp, ok := e.dispatchOne(cmd); ok {
			responses = append(responses, resp)
		}
	}

	if len(responses) == 0 {
		return "", false
	}
	return strings.Join(responses, ";"), true
}

// dispatchOne dispatches a single (non-compound) command.
func (e *CommandEngine) dispatchOne(command string) (string, bool) {
	normalized := normalizeCommand(command)
	upper := strings.ToUpper(normalized)

	e.handlersMu.RLock()
	var bestHandler Handler
	var bestPrefix string
	bestLen := -1
	for _, entry := range e.handlers {
		if strings.HasPrefix(upper, entry.prefix) && len(entry.prefix) > bestLen {
			bestHandler = entry.handler
			bestPrefix = entry.prefix
			bestLen = len(entry.prefix)
		}
	}
	e.handlersMu.RUnlock()

	if bestHandler != nil {
		cmd := newSCPICommand(normalized, bestPrefix)
		return bestHandler(cmd)
	}
	return e.handleProperty(normalized)
}

// normalizeCommand strips the leading colon and collapses consecutive colons.
func normalizeCommand(command string) string {
	s := strings.TrimLeft(command, ":")
	for strings.Contains(s, "::") {
		s = strings.ReplaceAll(s, "::", ":")
	}
	return s
}

// -- IEEE 488.2 handlers (called without holding any lock) -------------------

func (e *CommandEngine) handleIDN(_ *SCPICommand) (string, bool) {
	return e.Manufacturer + "," + e.Model + "," + e.Serial + "," + e.Firmware, true
}

func (e *CommandEngine) handleRST(_ *SCPICommand) (string, bool) {
	// Collect hooks before clearing so they fire after state is reset
	e.mu.Lock()
	e.stb = 0
	e.esr = 0
	e.opc = false
	e.srqPending = false
	e.properties = make(map[string]string)
	e.setDefaultProperties()
	hooks := make([]func(), len(e.resetHooks))
	copy(hooks, e.resetHooks)
	e.mu.Unlock()

	for _, h := range hooks {
		h()
	}
	return "", false
}

func (e *CommandEngine) handleCLS(_ *SCPICommand) (string, bool) {
	e.mu.Lock()
	e.stb = 0
	e.esr = 0
	e.srqPending = false
	e.mu.Unlock()
	return "", false
}

func (e *CommandEngine) handleSTBQuery(_ *SCPICommand) (string, bool) {
	e.mu.Lock()
	stb := e.getSTBLocked()
	e.mu.Unlock()
	return itoa(int(stb)), true
}

func (e *CommandEngine) handleESRQuery(_ *SCPICommand) (string, bool) {
	e.mu.Lock()
	val := e.esr
	e.esr = 0
	e.mu.Unlock()
	return itoa(int(val)), true
}

func (e *CommandEngine) handleESE(cmd *SCPICommand) (string, bool) {
	if cmd.Query {
		e.mu.Lock()
		v := e.ese
		e.mu.Unlock()
		return itoa(int(v)), true
	}
	val := cmd.ArgInt(0, -1)
	if val >= 0 {
		e.mu.Lock()
		e.ese = uint8(val & 0xFF)
		e.mu.Unlock()
	}
	return "", false
}

func (e *CommandEngine) handleSRE(cmd *SCPICommand) (string, bool) {
	if cmd.Query {
		e.mu.Lock()
		v := e.sre
		e.mu.Unlock()
		return itoa(int(v)), true
	}
	val := cmd.ArgInt(0, -1)
	if val >= 0 {
		e.mu.Lock()
		e.sre = uint8(val & 0xFF)
		e.mu.Unlock()
	}
	return "", false
}

func (e *CommandEngine) handleOPC(_ *SCPICommand) (string, bool) {
	e.mu.Lock()
	e.opc = true
	e.mu.Unlock()
	return "", false
}

func (e *CommandEngine) handleMeasure(cmd *SCPICommand) (string, bool) {
	upper := strings.ToUpper(cmd.Raw)
	upper = strings.ReplaceAll(upper, "MEASURE:", "MEAS:")
	parts := strings.Split(upper, ":")
	if len(parts) >= 2 {
		measType := parts[1]
		measType = strings.TrimSuffix(measType, "?")
		measType = strings.SplitN(measType, " ", 2)[0]
		e.mu.Lock()
		v, ok := e.properties[measType]
		e.mu.Unlock()
		if ok {
			return v, true
		}
	}
	return "9.90000E+37", true
}

// handleProperty is the fallback: treat as property get (with ?) or set.
func (e *CommandEngine) handleProperty(command string) (string, bool) {
	upper := strings.TrimSpace(strings.ToUpper(command))
	if strings.Contains(upper, "?") {
		key := strings.ReplaceAll(upper, "?", "")
		key = strings.TrimSpace(key)
		for _, candidate := range []string{key, strings.ReplaceAll(key, ":", "")} {
			e.mu.Lock()
			v, ok := e.properties[candidate]
			e.mu.Unlock()
			if ok {
				return v, true
			}
		}
		return "", false
	}
	parts := strings.SplitN(command, " ", 2)
	if len(parts) == 2 {
		e.mu.Lock()
		e.properties[strings.ToUpper(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		e.mu.Unlock()
	}
	return "", false
}

func (e *CommandEngine) getSTBLocked() uint8 {
	stb := e.stb
	if e.srqPending {
		stb |= 0x40
	}
	return stb
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
