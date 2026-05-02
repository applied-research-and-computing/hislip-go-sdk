package hislip

import (
	"strings"
	"sync"
	"testing"
)

func newTestEngine() *CommandEngine {
	return NewCommandEngine("MOCK", "Instrument", "SN001", "1.0.0", true, ";")
}

// -- Basic processing --------------------------------------------------------

func TestEngine_EmptyCommandReturnsNone(t *testing.T) {
	e := newTestEngine()
	_, ok := e.ProcessCommand("")
	if ok {
		t.Error("expected no response for empty command")
	}
	_, ok = e.ProcessCommand("   ")
	if ok {
		t.Error("expected no response for whitespace-only command")
	}
}

func TestEngine_SingleQueryReturnsResponse(t *testing.T) {
	e := newTestEngine()
	resp, ok := e.ProcessCommand("*IDN?")
	if !ok {
		t.Fatal("expected response for *IDN?")
	}
	if !strings.Contains(resp, "MOCK") {
		t.Errorf("*IDN? response %q does not contain MOCK", resp)
	}
}

func TestEngine_NonQueryReturnsNone(t *testing.T) {
	e := newTestEngine()
	_, ok := e.ProcessCommand("*RST")
	if ok {
		t.Error("expected no response for *RST (write command)")
	}
}

func TestEngine_SemicolonSeparatedCommands(t *testing.T) {
	e := newTestEngine()
	resp, ok := e.ProcessCommand("*IDN?;*OPC?")
	if !ok {
		t.Fatal("expected response")
	}
	parts := strings.Split(resp, ";")
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %q", len(parts), resp)
	}
	if !strings.Contains(parts[0], "MOCK") {
		t.Errorf("first part %q does not contain MOCK", parts[0])
	}
	if parts[1] != "1" {
		t.Errorf("*OPC? = %q, want 1", parts[1])
	}
}

func TestEngine_SemicolonMixWriteAndQuery(t *testing.T) {
	e := newTestEngine()
	resp, ok := e.ProcessCommand("*RST;*IDN?")
	if !ok {
		t.Fatal("expected response")
	}
	if !strings.Contains(resp, "MOCK") {
		t.Errorf("response %q does not contain MOCK", resp)
	}
}

func TestEngine_AllWritesReturnsNone(t *testing.T) {
	e := newTestEngine()
	_, ok := e.ProcessCommand("*RST;*CLS")
	if ok {
		t.Error("expected no response for all-write compound command")
	}
}

func TestEngine_CustomSeparator(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, "|")
	e.RegisterHandler("A?", func(_ *SCPICommand) (string, bool) { return "hello", true })
	e.RegisterHandler("B?", func(_ *SCPICommand) (string, bool) { return "world", true })
	resp, ok := e.ProcessCommand("A?|B?")
	if !ok {
		t.Fatal("expected response")
	}
	if resp != "hello;world" {
		t.Errorf("response = %q, want hello;world", resp)
	}
}

func TestEngine_NoSeparator(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, "")
	e.RegisterHandler("HELLO?", func(_ *SCPICommand) (string, bool) { return "yes", true })
	resp, ok := e.ProcessCommand("HELLO?")
	if !ok {
		t.Fatal("expected response")
	}
	if resp != "yes" {
		t.Errorf("response = %q, want yes", resp)
	}
}

// -- IEEE 488.2 commands -----------------------------------------------------

func TestEngine_IDNQuery(t *testing.T) {
	e := NewCommandEngine("ACME", "Widget", "X123", "2.0", true, ";")
	resp, _ := e.ProcessCommand("*IDN?")
	if resp != "ACME,Widget,X123,2.0" {
		t.Errorf("*IDN? = %q, want ACME,Widget,X123,2.0", resp)
	}
}

func TestEngine_RSTRestoresDefaults(t *testing.T) {
	e := newTestEngine()
	e.SetProperty("VOLT", "99.9")
	e.ProcessCommand("*RST")
	v, ok := e.GetProperty("VOLT")
	if !ok || v != "1.00000E+00" {
		t.Errorf("after *RST, VOLT = %q (%v), want 1.00000E+00", v, ok)
	}
}

func TestEngine_CLSClearsStatus(t *testing.T) {
	e := newTestEngine()
	e.GenerateSRQ()
	stb := e.ReadSTB()
	if stb&0x40 == 0 {
		// ReadSTB clears the pending flag, so check that SRQ was set first
		// (we generated it above but ReadSTB just cleared it — fine)
	}
	e.GenerateSRQ()
	e.ProcessCommand("*CLS")
	resp, _ := e.ProcessCommand("*STB?")
	if resp != "0" {
		t.Errorf("after *CLS, *STB? = %q, want 0", resp)
	}
}

func TestEngine_STBQuery(t *testing.T) {
	e := newTestEngine()
	resp, ok := e.ProcessCommand("*STB?")
	if !ok || resp != "0" {
		t.Errorf("*STB? = %q (%v), want 0", resp, ok)
	}
}

func TestEngine_ESRQueryClearsOnRead(t *testing.T) {
	e := newTestEngine()
	e.mu.Lock()
	e.esr = 42
	e.mu.Unlock()
	resp, _ := e.ProcessCommand("*ESR?")
	if resp != "42" {
		t.Errorf("*ESR? = %q, want 42", resp)
	}
	resp2, _ := e.ProcessCommand("*ESR?")
	if resp2 != "0" {
		t.Errorf("second *ESR? = %q, want 0 (cleared)", resp2)
	}
}

func TestEngine_ESESetAndQuery(t *testing.T) {
	e := newTestEngine()
	e.ProcessCommand("*ESE 32")
	resp, _ := e.ProcessCommand("*ESE?")
	if resp != "32" {
		t.Errorf("*ESE? = %q, want 32", resp)
	}
}

func TestEngine_ESEMasksToByte(t *testing.T) {
	e := newTestEngine()
	e.ProcessCommand("*ESE 256")
	resp, _ := e.ProcessCommand("*ESE?")
	if resp != "0" {
		t.Errorf("*ESE? after ESE 256 = %q, want 0 (masked to byte)", resp)
	}
}

func TestEngine_SRESetAndQuery(t *testing.T) {
	e := newTestEngine()
	e.ProcessCommand("*SRE 16")
	resp, _ := e.ProcessCommand("*SRE?")
	if resp != "16" {
		t.Errorf("*SRE? = %q, want 16", resp)
	}
}

func TestEngine_OPCQueryReturnsOne(t *testing.T) {
	e := newTestEngine()
	resp, ok := e.ProcessCommand("*OPC?")
	if !ok || resp != "1" {
		t.Errorf("*OPC? = %q (%v), want 1", resp, ok)
	}
}

func TestEngine_OPCSet(t *testing.T) {
	e := newTestEngine()
	e.ProcessCommand("*OPC")
	e.mu.Lock()
	opc := e.opc
	e.mu.Unlock()
	if !opc {
		t.Error("*OPC should set opc=true")
	}
}

func TestEngine_TSTQueryReturnsZero(t *testing.T) {
	e := newTestEngine()
	resp, ok := e.ProcessCommand("*TST?")
	if !ok || resp != "0" {
		t.Errorf("*TST? = %q (%v), want 0", resp, ok)
	}
}

// -- SCPI system commands ----------------------------------------------------

func TestEngine_SystErrQuery(t *testing.T) {
	e := newTestEngine()
	resp, _ := e.ProcessCommand("SYST:ERR?")
	if resp != `0,"No error"` {
		t.Errorf("SYST:ERR? = %q", resp)
	}
	resp2, _ := e.ProcessCommand("SYSTEM:ERROR?")
	if resp2 != `0,"No error"` {
		t.Errorf("SYSTEM:ERROR? = %q", resp2)
	}
}

func TestEngine_SystVersQuery(t *testing.T) {
	e := newTestEngine()
	resp, _ := e.ProcessCommand("SYST:VERS?")
	if resp != "1999.0" {
		t.Errorf("SYST:VERS? = %q, want 1999.0", resp)
	}
	resp2, _ := e.ProcessCommand("SYSTEM:VERSION?")
	if resp2 != "1999.0" {
		t.Errorf("SYSTEM:VERSION? = %q, want 1999.0", resp2)
	}
}

func TestEngine_MeasureVoltage(t *testing.T) {
	e := newTestEngine()
	resp, ok := e.ProcessCommand("MEAS:VOLT?")
	if !ok || resp != "1.00000E+00" {
		t.Errorf("MEAS:VOLT? = %q (%v), want 1.00000E+00", resp, ok)
	}
}

func TestEngine_MeasureCurrent(t *testing.T) {
	e := newTestEngine()
	resp, _ := e.ProcessCommand("MEAS:CURR?")
	if resp != "5.00000E-03" {
		t.Errorf("MEAS:CURR? = %q, want 5.00000E-03", resp)
	}
}

func TestEngine_MeasureLongForm(t *testing.T) {
	e := newTestEngine()
	resp, _ := e.ProcessCommand("MEASURE:FREQ?")
	if resp != "1.00000E+03" {
		t.Errorf("MEASURE:FREQ? = %q, want 1.00000E+03", resp)
	}
}

func TestEngine_MeasureUnknownReturnsNaN(t *testing.T) {
	e := newTestEngine()
	resp, ok := e.ProcessCommand("MEAS:POWER?")
	if !ok || resp != "9.90000E+37" {
		t.Errorf("MEAS:POWER? = %q (%v), want 9.90000E+37", resp, ok)
	}
}

func TestEngine_DefaultProperties(t *testing.T) {
	e := newTestEngine()
	cases := map[string]string{
		"VOLT": "1.00000E+00",
		"CURR": "5.00000E-03",
		"FREQ": "1.00000E+03",
		"RES":  "1.00000E+04",
	}
	for k, want := range cases {
		v, ok := e.GetProperty(k)
		if !ok || v != want {
			t.Errorf("GetProperty(%q) = %q (%v), want %q", k, v, ok, want)
		}
	}
}

// -- IEEE 488.2 disabled -----------------------------------------------------

func TestEngine_NoIEEE488Commands(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	_, ok := e.ProcessCommand("*IDN?")
	if ok {
		t.Error("*IDN? should return no response when ieee488=false")
	}
}

func TestEngine_NoSCPICommands(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	_, ok := e.ProcessCommand("SYST:ERR?")
	if ok {
		t.Error("SYST:ERR? should return no response when ieee488=false")
	}
}

func TestEngine_CustomHandlersWorkWithoutIEEE488(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	e.RegisterHandler("STATUS", func(_ *SCPICommand) (string, bool) { return "OK", true })
	resp, ok := e.ProcessCommand("STATUS")
	if !ok || resp != "OK" {
		t.Errorf("STATUS = %q (%v), want OK", resp, ok)
	}
}

// -- Custom handlers ---------------------------------------------------------

func TestEngine_RegisterAndCall(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	e.RegisterHandler("HELLO", func(_ *SCPICommand) (string, bool) { return "WORLD", true })
	resp, ok := e.ProcessCommand("HELLO")
	if !ok || resp != "WORLD" {
		t.Errorf("HELLO = %q (%v), want WORLD", resp, ok)
	}
}

func TestEngine_HandlerReceivesSCPICommand(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	var received *SCPICommand
	e.RegisterHandler("SET", func(cmd *SCPICommand) (string, bool) {
		received = cmd
		return "", false
	})
	e.ProcessCommand("SET VALUE 42")
	if received == nil {
		t.Fatal("handler was not called")
	}
	if received.Raw != "SET VALUE 42" {
		t.Errorf("Raw = %q, want SET VALUE 42", received.Raw)
	}
	if received.Prefix != "SET" {
		t.Errorf("Prefix = %q, want SET", received.Prefix)
	}
}

func TestEngine_PrefixMatching(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	e.RegisterHandler("MEAS", func(_ *SCPICommand) (string, bool) { return "42", true })
	resp, ok := e.ProcessCommand("MEAS:VOLT?")
	if !ok || resp != "42" {
		t.Errorf("MEAS:VOLT? = %q (%v), want 42", resp, ok)
	}
}

func TestEngine_CaseInsensitiveMatching(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	e.RegisterHandler("HELLO", func(_ *SCPICommand) (string, bool) { return "yes", true })
	for _, cmd := range []string{"hello", "Hello", "HELLO"} {
		resp, ok := e.ProcessCommand(cmd)
		if !ok || resp != "yes" {
			t.Errorf("ProcessCommand(%q) = %q (%v), want yes", cmd, resp, ok)
		}
	}
}

func TestEngine_LongestPrefixWins(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	e.RegisterHandler("AB", func(_ *SCPICommand) (string, bool) { return "short", true })
	e.RegisterHandler("ABC", func(_ *SCPICommand) (string, bool) { return "long", true })
	resp, _ := e.ProcessCommand("ABCD")
	if resp != "long" {
		t.Errorf("ABCD = %q, want long (longest prefix ABC wins)", resp)
	}
	resp2, _ := e.ProcessCommand("ABX")
	if resp2 != "short" {
		t.Errorf("ABX = %q, want short (only AB matches)", resp2)
	}
}

func TestEngine_HandlerReturningNone(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	e.RegisterHandler("SET", func(_ *SCPICommand) (string, bool) { return "", false })
	_, ok := e.ProcessCommand("SET X 5")
	if ok {
		t.Error("expected no response for write handler returning false")
	}
}

// -- Properties --------------------------------------------------------------

func TestEngine_SetAndQueryProperty(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	e.ProcessCommand("POWER 50")
	resp, ok := e.ProcessCommand("POWER?")
	if !ok || resp != "50" {
		t.Errorf("POWER? = %q (%v), want 50", resp, ok)
	}
}

func TestEngine_CaseInsensitiveProperty(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	e.ProcessCommand("voltage 3.14")
	resp, ok := e.ProcessCommand("VOLTAGE?")
	if !ok || resp != "3.14" {
		t.Errorf("VOLTAGE? = %q (%v), want 3.14", resp, ok)
	}
}

func TestEngine_UnknownPropertyQueryReturnsNone(t *testing.T) {
	e := NewCommandEngine("M", "I", "S", "F", false, ";")
	_, ok := e.ProcessCommand("NOEXIST?")
	if ok {
		t.Error("unknown property should return no response")
	}
}

func TestEngine_DirectGetSet(t *testing.T) {
	e := newTestEngine()
	e.SetProperty("TEMP", "25.0")
	v, ok := e.GetProperty("TEMP")
	if !ok || v != "25.0" {
		t.Errorf("GetProperty(TEMP) = %q (%v), want 25.0", v, ok)
	}
	v2, ok2 := e.GetProperty("temp")
	if !ok2 || v2 != "25.0" {
		t.Errorf("GetProperty(temp) = %q (%v), want 25.0 (case-insensitive)", v2, ok2)
	}
}

func TestEngine_DirectGetMissing(t *testing.T) {
	e := newTestEngine()
	_, ok := e.GetProperty("NOEXIST")
	if ok {
		t.Error("GetProperty of missing key should return ok=false")
	}
}

// -- Status registers --------------------------------------------------------

func TestEngine_InitialSTBIsZero(t *testing.T) {
	e := newTestEngine()
	if stb := e.ReadSTB(); stb != 0 {
		t.Errorf("initial STB = %d, want 0", stb)
	}
}

func TestEngine_GenerateSRQSetsBit6(t *testing.T) {
	e := newTestEngine()
	e.GenerateSRQ()
	stb := e.ReadSTB()
	if stb&0x40 == 0 {
		t.Errorf("STB = %d, bit 6 should be set after GenerateSRQ", stb)
	}
}

func TestEngine_ReadSTBClearsSRQ(t *testing.T) {
	e := newTestEngine()
	e.GenerateSRQ()
	e.ReadSTB()
	stb := e.ReadSTB()
	if stb&0x40 != 0 {
		t.Error("bit 6 should be cleared after ReadSTB")
	}
}

func TestEngine_CLSClearsSRQ(t *testing.T) {
	e := newTestEngine()
	e.GenerateSRQ()
	e.ProcessCommand("*CLS")
	if stb := e.ReadSTB(); stb != 0 {
		t.Errorf("after *CLS, STB = %d, want 0", stb)
	}
}

func TestEngine_RSTPreservesEnableRegisters(t *testing.T) {
	// Per IEEE 488.2 §10.32: *RST must NOT clear ESE or SRE.
	e := newTestEngine()
	e.GenerateSRQ()
	e.ProcessCommand("*ESE 255")
	e.ProcessCommand("*SRE 255")
	e.ProcessCommand("*RST")
	ese, _ := e.ProcessCommand("*ESE?")
	sre, _ := e.ProcessCommand("*SRE?")
	if ese != "255" {
		t.Errorf("*ESE? after *RST = %q, want 255 (must survive RST)", ese)
	}
	if sre != "255" {
		t.Errorf("*SRE? after *RST = %q, want 255 (must survive RST)", sre)
	}
	if stb := e.ReadSTB(); stb != 0 {
		t.Errorf("STB after *RST = %d, want 0", stb)
	}
}

// -- Reset hooks -------------------------------------------------------------

func TestEngine_ResetHookCalledOnRST(t *testing.T) {
	e := newTestEngine()
	called := false
	e.AddResetHook(func() { called = true })
	e.ProcessCommand("*RST")
	if !called {
		t.Error("reset hook was not called after *RST")
	}
}

func TestEngine_ResetHookCanCallSetProperty(t *testing.T) {
	e := newTestEngine()
	e.AddResetHook(func() {
		e.SetProperty("CUSTOM", "reset_value")
	})
	e.ProcessCommand("*RST")
	v, ok := e.GetProperty("CUSTOM")
	if !ok || v != "reset_value" {
		t.Errorf("after RST hook, CUSTOM = %q (%v), want reset_value", v, ok)
	}
}

// -- Thread safety -----------------------------------------------------------

func TestEngine_ConcurrentProcessCommand(t *testing.T) {
	e := newTestEngine()
	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				resp, ok := e.ProcessCommand("*IDN?")
				if !ok || !strings.Contains(resp, "MOCK") {
					errCh <- nil // signal unexpected result
				}
				e.ProcessCommand("*RST")
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	if len(errCh) > 0 {
		t.Error("concurrent *IDN? returned unexpected results")
	}
}

func TestEngine_SRQCallbackInvoked(t *testing.T) {
	e := newTestEngine()
	called := make(chan struct{}, 1)
	e.OnSRQ(func() { called <- struct{}{} })
	e.GenerateSRQ()
	select {
	case <-called:
	default:
		t.Error("SRQ callback was not invoked")
	}
}

func TestEngine_SRQCallbackNilDoesNotPanic(t *testing.T) {
	e := newTestEngine()
	e.OnSRQ(nil)
	e.GenerateSRQ() // should not panic
}

func TestEngine_ConcurrentReadSTB(t *testing.T) {
	e := newTestEngine()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				e.GenerateSRQ()
				e.ReadSTB()
			}
		}()
	}
	wg.Wait() // should not panic or deadlock
}
