package hislip

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test helpers (mirrors Python conftest / helper functions)
// ---------------------------------------------------------------------------

func makeTestInstrument(t *testing.T) (*minimalInstrument, *HiSLIPProtocol) {
	t.Helper()
	proto := NewHiSLIPProtocol("127.0.0.1", 0, "hislip0")
	inst := newMinimalInstrument(proto)
	if err := inst.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	return inst, proto
}

func sendTestMsg(t *testing.T, conn net.Conn, msgType, controlCode uint8, msgParam uint32, payload []byte) {
	t.Helper()
	if err := sendMsg(conn, msgType, controlCode, msgParam, payload); err != nil {
		t.Fatalf("sendMsg: %v", err)
	}
}

func recvTestMsg(t *testing.T, conn net.Conn) *hislipMsg {
	t.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	msg, err := recvMsg(conn, 0)
	conn.SetDeadline(time.Time{})
	if err != nil {
		t.Fatalf("recvMsg: %v", err)
	}
	return msg
}

// hislipConnect establishes a full HiSLIP session (sync + async channels),
// mirroring the Python _hislip_connect helper.
func hislipConnect(t *testing.T, host string, port int) (syncConn, asyncConn net.Conn, sessionID uint16) {
	t.Helper()
	// Sync channel
	syncConn, err := net.Dial("tcp", net.JoinHostPort(host, itoa(port)))
	if err != nil {
		t.Fatalf("sync dial: %v", err)
	}
	sendTestMsg(t, syncConn, msgInitialize, 0, 0, []byte("hislip0"))
	msg := recvTestMsg(t, syncConn)
	if msg.msgType != msgInitializeResponse {
		t.Fatalf("expected InitializeResponse, got %d", msg.msgType)
	}
	sessionID = uint16(msg.msgParam & 0xFFFF)

	// Async channel
	asyncConn, err = net.Dial("tcp", net.JoinHostPort(host, itoa(port)))
	if err != nil {
		t.Fatalf("async dial: %v", err)
	}
	sendTestMsg(t, asyncConn, msgAsyncInitialize, 0, uint32(sessionID), nil)
	msg = recvTestMsg(t, asyncConn)
	if msg.msgType != msgAsyncInitializeResponse {
		t.Fatalf("expected AsyncInitializeResponse, got %d", msg.msgType)
	}
	return
}

// ---------------------------------------------------------------------------
// TestHiSLIPLifecycle
// ---------------------------------------------------------------------------

func TestHiSLIP_StartAndStop(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	if proto.Port == 0 {
		t.Error("port should be non-zero after Start")
	}
	inst.Stop()
}

func TestHiSLIP_ResourceString(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()
	rs := proto.ResourceString()
	want := "TCPIP0::127.0.0.1::hislip0::INSTR"
	if rs != want {
		t.Errorf("ResourceString = %q, want %q", rs, want)
	}
}

// ---------------------------------------------------------------------------
// TestHiSLIPInitialize
// ---------------------------------------------------------------------------

func TestHiSLIP_SyncInitialize(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	conn, err := net.Dial("tcp", net.JoinHostPort(proto.Host, itoa(proto.Port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sendTestMsg(t, conn, msgInitialize, 0, 0, []byte("hislip0"))
	msg := recvTestMsg(t, conn)

	if msg.msgType != msgInitializeResponse {
		t.Fatalf("msg type = %d, want %d (InitializeResponse)", msg.msgType, msgInitializeResponse)
	}
	serverVersion := uint16(msg.msgParam >> 16)
	sessID := uint16(msg.msgParam & 0xFFFF)
	if serverVersion != 0x0100 {
		t.Errorf("server version = 0x%04X, want 0x0100", serverVersion)
	}
	if sessID < 1 {
		t.Errorf("session ID = %d, want >= 1", sessID)
	}
}

func TestHiSLIP_AsyncInitialize(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, sessID := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	if sessID < 1 {
		t.Errorf("session ID = %d, want >= 1", sessID)
	}
}

func TestHiSLIP_AsyncInitializeReturnsVendorID(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, err := net.Dial("tcp", net.JoinHostPort(proto.Host, itoa(proto.Port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer syncConn.Close()

	sendTestMsg(t, syncConn, msgInitialize, 0, 0, []byte("hislip0"))
	msg := recvTestMsg(t, syncConn)
	sessID := uint16(msg.msgParam & 0xFFFF)

	asyncConn, err := net.Dial("tcp", net.JoinHostPort(proto.Host, itoa(proto.Port)))
	if err != nil {
		t.Fatalf("async dial: %v", err)
	}
	defer asyncConn.Close()

	sendTestMsg(t, asyncConn, msgAsyncInitialize, 0, uint32(sessID), nil)
	msg = recvTestMsg(t, asyncConn)

	if msg.msgType != msgAsyncInitializeResponse {
		t.Fatalf("msg type = %d, want AsyncInitializeResponse", msg.msgType)
	}
	if msg.msgParam != VendorID {
		t.Errorf("vendor ID = 0x%08X, want 0x%08X", msg.msgParam, VendorID)
	}
}

// ---------------------------------------------------------------------------
// TestHiSLIPCommands
// ---------------------------------------------------------------------------

func TestHiSLIP_IDNQuery(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, syncConn, msgDataEnd, 1, 1, []byte("*IDN?\n"))
	msg := recvTestMsg(t, syncConn)

	if msg.msgType != msgDataEnd {
		t.Fatalf("msg type = %d, want DataEnd", msg.msgType)
	}
	resp := string(msg.payload)
	// trim trailing newline
	for len(resp) > 0 && (resp[len(resp)-1] == '\n' || resp[len(resp)-1] == '\r') {
		resp = resp[:len(resp)-1]
	}
	if resp != "TEST,INST,SN001,1.0" {
		t.Errorf("*IDN? = %q, want TEST,INST,SN001,1.0", resp)
	}
}

func TestHiSLIP_OPCQuery(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, syncConn, msgDataEnd, 1, 2, []byte("*OPC?\n"))
	msg := recvTestMsg(t, syncConn)
	resp := trimNewline(string(msg.payload))
	if resp != "1" {
		t.Errorf("*OPC? = %q, want 1", resp)
	}
}

func TestHiSLIP_WriteCommandNoResponse(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, syncConn, msgDataEnd, 1, 3, []byte("*RST\n"))
	// No DataEnd expected; send a follow-up query to verify server is alive
	sendTestMsg(t, syncConn, msgDataEnd, 1, 4, []byte("*OPC?\n"))
	msg := recvTestMsg(t, syncConn)
	if msg == nil || trimNewline(string(msg.payload)) != "1" {
		t.Error("server did not respond to *OPC? after *RST write")
	}
}

// ---------------------------------------------------------------------------
// TestHiSLIPAsync
// ---------------------------------------------------------------------------

func TestHiSLIP_MaxMessageSizeNegotiation(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	proposed := uint64(2 * 1024 * 1024) // 2 MB
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, proposed)
	sendTestMsg(t, asyncConn, msgAsyncMaxMsgSize, 0, 0, payload)

	msg := recvTestMsg(t, asyncConn)
	if msg.msgType != msgAsyncMaxMsgSizeResp {
		t.Fatalf("msg type = %d, want AsyncMaxMsgSizeResponse", msg.msgType)
	}
	negotiated := binary.BigEndian.Uint64(msg.payload)
	if negotiated == 0 || negotiated > proposed {
		t.Errorf("negotiated size = %d, want > 0 and <= %d", negotiated, proposed)
	}
}

func TestHiSLIP_StatusQuery(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, asyncConn, msgAsyncStatusQuery, 0, 1, nil)
	msg := recvTestMsg(t, asyncConn)
	if msg.msgType != msgAsyncStatusResponse {
		t.Fatalf("msg type = %d, want AsyncStatusResponse", msg.msgType)
	}
}

func TestHiSLIP_StatusAfterQueryHasMAV(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	// Generate a response (sets MAV)
	sendTestMsg(t, syncConn, msgDataEnd, 1, 1, []byte("*IDN?\n"))
	recvTestMsg(t, syncConn) // consume response

	// Check status
	sendTestMsg(t, asyncConn, msgAsyncStatusQuery, 0, 2, nil)
	msg := recvTestMsg(t, asyncConn)
	stb := msg.controlCode
	if stb&0x10 == 0 {
		t.Errorf("STB = 0x%02X, bit 4 (MAV) should be set after a query response", stb)
	}
}

// ---------------------------------------------------------------------------
// TestHiSLIPErrorHandling
// ---------------------------------------------------------------------------

func TestHiSLIP_WrongSubAddressGetsFatalError(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	conn, _ := net.Dial("tcp", net.JoinHostPort(proto.Host, itoa(proto.Port)))
	defer conn.Close()

	sendTestMsg(t, conn, msgInitialize, 0, 0, []byte("hislip99"))
	msg := recvTestMsg(t, conn)
	if msg.msgType != msgFatalError {
		t.Errorf("msg type = %d, want FatalError", msg.msgType)
	}
	if msg.controlCode != fatalUnidentified {
		t.Errorf("controlCode = %d, want fatalUnidentified(%d)", msg.controlCode, fatalUnidentified)
	}
}

func TestHiSLIP_InvalidFirstMessageGetsFatalError(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	conn, _ := net.Dial("tcp", net.JoinHostPort(proto.Host, itoa(proto.Port)))
	defer conn.Close()

	sendTestMsg(t, conn, msgDataEnd, 0, 0, []byte("*IDN?\n"))
	msg := recvTestMsg(t, conn)
	if msg.msgType != msgFatalError {
		t.Errorf("msg type = %d, want FatalError", msg.msgType)
	}
	if msg.controlCode != fatalBadMsgType {
		t.Errorf("controlCode = %d, want fatalBadMsgType(%d)", msg.controlCode, fatalBadMsgType)
	}
}

func TestHiSLIP_UnknownAsyncSessionGetsFatalError(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	conn, _ := net.Dial("tcp", net.JoinHostPort(proto.Host, itoa(proto.Port)))
	defer conn.Close()

	sendTestMsg(t, conn, msgAsyncInitialize, 0, 9999, nil)
	msg := recvTestMsg(t, conn)
	if msg.msgType != msgFatalError {
		t.Errorf("msg type = %d, want FatalError", msg.msgType)
	}
	if msg.controlCode != fatalUnidentified {
		t.Errorf("controlCode = %d, want fatalUnidentified", msg.controlCode)
	}
}

func TestHiSLIP_EngineExceptionDoesNotKillSession(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	// Register a panicking handler
	inst.Engine.RegisterHandler("BOOM", func(_ *SCPICommand) (string, bool) {
		panic("boom")
	})

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, syncConn, msgDataEnd, 1, 1, []byte("BOOM\n"))
	msg := recvTestMsg(t, syncConn)
	if msg.msgType != msgDataEnd {
		t.Fatalf("msg type = %d, want DataEnd (error response)", msg.msgType)
	}
	resp := trimNewline(string(msg.payload))
	if resp == "" {
		t.Error("expected non-empty error response after panic")
	}

	// Session should survive
	sendTestMsg(t, syncConn, msgDataEnd, 1, 2, []byte("*IDN?\n"))
	msg = recvTestMsg(t, syncConn)
	resp = trimNewline(string(msg.payload))
	if resp != "TEST,INST,SN001,1.0" {
		t.Errorf("after panic, *IDN? = %q, want TEST,INST,SN001,1.0", resp)
	}
}

// ---------------------------------------------------------------------------
// TestHiSLIPSessionCleanup
// ---------------------------------------------------------------------------

func TestHiSLIP_SessionRemovedAfterDisconnect(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, sessID := hislipConnect(t, proto.Host, proto.Port)

	proto.sessionsMu.Lock()
	_, exists := proto.sessions[sessID]
	proto.sessionsMu.Unlock()
	if !exists {
		t.Fatal("session should exist while connected")
	}

	syncConn.Close()
	asyncConn.Close()
	time.Sleep(200 * time.Millisecond)

	proto.sessionsMu.Lock()
	_, exists = proto.sessions[sessID]
	proto.sessionsMu.Unlock()
	if exists {
		t.Error("session should be removed after disconnect")
	}
}

func TestHiSLIP_StopClosesAllSessions(t *testing.T) {
	inst, proto := makeTestInstrument(t)

	sync1, async1, sid1 := hislipConnect(t, proto.Host, proto.Port)
	sync2, async2, sid2 := hislipConnect(t, proto.Host, proto.Port)

	proto.sessionsMu.Lock()
	_, ok1 := proto.sessions[sid1]
	_, ok2 := proto.sessions[sid2]
	proto.sessionsMu.Unlock()
	if !ok1 || !ok2 {
		t.Fatal("both sessions should exist before stop")
	}

	inst.Stop()
	time.Sleep(100 * time.Millisecond)

	proto.sessionsMu.Lock()
	count := len(proto.sessions)
	proto.sessionsMu.Unlock()
	if count != 0 {
		t.Errorf("sessions after Stop = %d, want 0", count)
	}

	for _, c := range []net.Conn{sync1, async1, sync2, async2} {
		c.Close()
	}
}

// ---------------------------------------------------------------------------
// TestHiSLIPLocking
// ---------------------------------------------------------------------------

func TestHiSLIP_ExclusiveLockGrant(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, asyncConn, msgAsyncLock, 1, 0, nil) // control_code=1 = exclusive
	msg := recvTestMsg(t, asyncConn)
	if msg.msgType != msgAsyncLockResponse {
		t.Fatalf("msg type = %d, want AsyncLockResponse", msg.msgType)
	}
	if msg.controlCode != 1 {
		t.Errorf("lock response code = %d, want 1 (success)", msg.controlCode)
	}
}

func TestHiSLIP_ExclusiveLockConflict(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	sync1, async1, _ := hislipConnect(t, proto.Host, proto.Port)
	sync2, async2, _ := hislipConnect(t, proto.Host, proto.Port)
	defer sync1.Close()
	defer async1.Close()
	defer sync2.Close()
	defer async2.Close()

	// Session 1 acquires
	sendTestMsg(t, async1, msgAsyncLock, 1, 0, nil)
	msg := recvTestMsg(t, async1)
	if msg.controlCode != 1 {
		t.Fatalf("session 1 lock failed: code=%d", msg.controlCode)
	}

	// Session 2 should fail
	sendTestMsg(t, async2, msgAsyncLock, 1, 0, nil)
	msg = recvTestMsg(t, async2)
	if msg.controlCode != 3 {
		t.Errorf("session 2 lock code = %d, want 3 (error/cannot grant)", msg.controlCode)
	}
}

func TestHiSLIP_LockReleaseAndReacquire(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	sync1, async1, _ := hislipConnect(t, proto.Host, proto.Port)
	sync2, async2, _ := hislipConnect(t, proto.Host, proto.Port)
	defer sync1.Close()
	defer async1.Close()
	defer sync2.Close()
	defer async2.Close()

	// Session 1 acquires
	sendTestMsg(t, async1, msgAsyncLock, 1, 0, nil)
	msg := recvTestMsg(t, async1)
	if msg.controlCode != 1 {
		t.Fatalf("acquire failed: %d", msg.controlCode)
	}

	// Session 1 releases (control_code=0)
	sendTestMsg(t, async1, msgAsyncLock, 0, 0, nil)
	msg = recvTestMsg(t, async1)
	if msg.controlCode != 1 {
		t.Errorf("release response = %d, want 1 (success)", msg.controlCode)
	}

	// Session 2 can now acquire
	sendTestMsg(t, async2, msgAsyncLock, 1, 0, nil)
	msg = recvTestMsg(t, async2)
	if msg.controlCode != 1 {
		t.Errorf("session 2 reacquire = %d, want 1", msg.controlCode)
	}
}

// ---------------------------------------------------------------------------
// TestHiSLIPDeviceClear
// ---------------------------------------------------------------------------

func TestHiSLIP_FullDeviceClearHandshake(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	// Step 1: client sends AsyncDeviceClear
	sendTestMsg(t, asyncConn, msgAsyncDeviceClear, 0, 0, nil)
	// Step 2: server responds with AsyncDeviceClearAck
	msg := recvTestMsg(t, asyncConn)
	if msg.msgType != msgAsyncDeviceClearAck {
		t.Fatalf("expected AsyncDeviceClearAck, got %d", msg.msgType)
	}
	// Step 3: client sends DeviceClearComplete on sync
	sendTestMsg(t, syncConn, msgDeviceClearComplete, 0, 0, nil)
	// Step 4: server responds with DeviceClearAck
	msg = recvTestMsg(t, syncConn)
	if msg.msgType != msgDeviceClearAck {
		t.Fatalf("expected DeviceClearAck, got %d", msg.msgType)
	}
}

func TestHiSLIP_DeviceClearResetsMessageID(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	// Send a command to advance message_id
	sendTestMsg(t, syncConn, msgDataEnd, 1, 42, []byte("*IDN?\n"))
	recvTestMsg(t, syncConn) // consume response

	// Device clear
	sendTestMsg(t, asyncConn, msgAsyncDeviceClear, 0, 0, nil)
	recvTestMsg(t, asyncConn) // AsyncDeviceClearAck
	sendTestMsg(t, syncConn, msgDeviceClearComplete, 0, 0, nil)
	recvTestMsg(t, syncConn) // DeviceClearAck (may also get AsyncInterrupted first)

	// Send another command — verify session still works
	sendTestMsg(t, syncConn, msgDataEnd, 1, 0xFFFFFF00, []byte("*OPC?\n"))
	msg := recvTestMsg(t, syncConn)
	if msg.msgType != msgDataEnd {
		t.Fatalf("expected DataEnd, got %d", msg.msgType)
	}
	if trimNewline(string(msg.payload)) != "1" {
		t.Errorf("*OPC? after device clear = %q, want 1", trimNewline(string(msg.payload)))
	}
}

// ---------------------------------------------------------------------------
// TestHiSLIPSRQ
// ---------------------------------------------------------------------------

func TestHiSLIP_SRQPushToAsyncChannel(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	inst.Engine.GenerateSRQ()

	asyncConn.SetDeadline(time.Now().Add(2 * time.Second))
	msg, err := recvMsg(asyncConn, 0)
	asyncConn.SetDeadline(time.Time{})
	if err != nil {
		t.Fatalf("recvMsg after SRQ: %v", err)
	}
	if msg.msgType != msgAsyncServiceRequest {
		t.Errorf("msg type = %d, want AsyncServiceRequest(%d)", msg.msgType, msgAsyncServiceRequest)
	}
}

// ---------------------------------------------------------------------------
// TestEventDecorators (on_trigger, on_clear)
// ---------------------------------------------------------------------------

func TestHiSLIP_OnTriggerDecorator(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	triggered := make(chan struct{}, 1)
	proto.OnTrigger(func() { triggered <- struct{}{} })

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, syncConn, msgTrigger, 0, 1, nil)

	select {
	case <-triggered:
	case <-time.After(2 * time.Second):
		t.Error("on_trigger callback was not invoked within 2 seconds")
	}
}

func TestHiSLIP_OnClearDecorator(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	cleared := make(chan struct{}, 1)
	proto.OnClear(func() { cleared <- struct{}{} })

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, asyncConn, msgAsyncDeviceClear, 0, 0, nil)
	recvTestMsg(t, asyncConn) // AsyncDeviceClearAck
	sendTestMsg(t, syncConn, msgDeviceClearComplete, 0, 0, nil)
	recvTestMsg(t, syncConn) // DeviceClearAck

	select {
	case <-cleared:
	case <-time.After(2 * time.Second):
		t.Error("on_clear callback was not invoked within 2 seconds")
	}
}

func TestHiSLIP_TriggerWithoutCallback(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
	defer syncConn.Close()
	defer asyncConn.Close()

	sendTestMsg(t, syncConn, msgTrigger, 0, 1, nil)

	// Session should still be alive
	sendTestMsg(t, syncConn, msgDataEnd, 1, 2, []byte("*IDN?\n"))
	msg := recvTestMsg(t, syncConn)
	if trimNewline(string(msg.payload)) != "TEST,INST,SN001,1.0" {
		t.Errorf("*IDN? after trigger = %q", trimNewline(string(msg.payload)))
	}
}

// ---------------------------------------------------------------------------
// Multiple concurrent sessions
// ---------------------------------------------------------------------------

func TestHiSLIP_MultipleConcurrentSessions(t *testing.T) {
	inst, proto := makeTestInstrument(t)
	defer inst.Stop()

	const numSessions = 5
	var wg sync.WaitGroup
	errors := make(chan string, numSessions)

	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			syncConn, asyncConn, _ := hislipConnect(t, proto.Host, proto.Port)
			defer syncConn.Close()
			defer asyncConn.Close()

			for j := 0; j < 10; j++ {
				sendMsg(syncConn, msgDataEnd, 1, uint32(j+1), []byte("*IDN?\n"))
				syncConn.SetDeadline(time.Now().Add(2 * time.Second))
				msg, err := recvMsg(syncConn, 0)
				syncConn.SetDeadline(time.Time{})
				if err != nil {
					errors <- err.Error()
					return
				}
				resp := trimNewline(string(msg.payload))
				if resp != "TEST,INST,SN001,1.0" {
					errors <- "unexpected response: " + resp
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
