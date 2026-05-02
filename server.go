package hislip

import (
	"os"
	"os/signal"
	"syscall"
)

// HiSLIPServer is a Flask-style entry point for simple instruments that
// do not need the full InstrumentBase lifecycle.
//
//	srv := hislip.NewHiSLIPServer("ACME", "DMM100", "SN001", "1.0.0", true, ";", "127.0.0.1", 4880, "hislip0")
//	srv.Command("READ?", func(_ *hislip.SCPICommand) (string, bool) { return "1.234", true })
//	srv.Run()
type HiSLIPServer struct {
	Engine   *CommandEngine
	protocol *HiSLIPProtocol
	stopCh   chan struct{}
}

// NewHiSLIPServer creates a HiSLIPServer ready to accept command registrations.
func NewHiSLIPServer(
	manufacturer, model, serial, firmware string,
	ieee488 bool,
	commandSeparator string,
	host string,
	port int,
	subAddress string,
) *HiSLIPServer {
	engine := NewCommandEngine(manufacturer, model, serial, firmware, ieee488, commandSeparator)
	proto := NewHiSLIPProtocol(host, port, subAddress)
	proto.Attach(engine)
	return &HiSLIPServer{
		Engine:   engine,
		protocol: proto,
		stopCh:   make(chan struct{}),
	}
}

// Command registers a SCPI handler.
func (s *HiSLIPServer) Command(prefix string, handler Handler) {
	s.Engine.RegisterHandler(prefix, handler)
}

// OnTrigger registers a callback for HiSLIP trigger events.
func (s *HiSLIPServer) OnTrigger(fn func()) {
	s.protocol.OnTrigger(fn)
}

// OnClear registers a callback for HiSLIP device-clear events.
func (s *HiSLIPServer) OnClear(fn func()) {
	s.protocol.OnClear(fn)
}

// Start starts the server in the background (non-blocking).
func (s *HiSLIPServer) Start() error {
	return s.protocol.Start()
}

// Stop stops the server and signals Wait to return.
func (s *HiSLIPServer) Stop() {
	s.protocol.Stop()
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// Wait blocks until Stop is called or SIGINT/SIGTERM is received.
func (s *HiSLIPServer) Wait() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	select {
	case <-s.stopCh:
	case <-sigCh:
		s.Stop()
	}
	signal.Stop(sigCh)
}

// Run starts the server and blocks until interrupted.
// Equivalent to Start() followed by Wait().
func (s *HiSLIPServer) Run() error {
	if err := s.Start(); err != nil {
		return err
	}
	s.Wait()
	return nil
}

// Host returns the bind address.
func (s *HiSLIPServer) Host() string { return s.protocol.Host }

// Port returns the listen port (valid after Start).
func (s *HiSLIPServer) Port() int { return s.protocol.Port }

// SubAddress returns the HiSLIP sub-address.
func (s *HiSLIPServer) SubAddress() string { return s.protocol.SubAddress }

// ResourceString returns the VISA resource string for this server.
func (s *HiSLIPServer) ResourceString() string { return s.protocol.ResourceString() }
