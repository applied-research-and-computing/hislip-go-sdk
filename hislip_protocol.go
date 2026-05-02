package hislip

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

// Protocol is the interface implemented by instrument communication backends.
type Protocol interface {
	// Attach gives the protocol a CommandEngine to dispatch commands.
	Attach(engine *CommandEngine)
	// Start begins accepting client connections. Non-blocking.
	Start() error
	// Stop stops the server and releases resources.
	Stop()
	// ResourceString returns the VISA-style resource string (VPP-4.3).
	ResourceString() string
}

// HiSLIPProtocol implements the HiSLIP (IVI-6.1) server over TCP.
type HiSLIPProtocol struct {
	Host       string
	Port       int
	SubAddress string

	engine     *CommandEngine
	listener   net.Listener
	running    atomic.Bool

	sessionsMu    sync.Mutex
	sessions      map[uint16]*hislipSession
	nextSessionID uint16

	lockMgr   lockManager
	onTrigger func()
	onClear   func()
}

// NewHiSLIPProtocol creates a HiSLIPProtocol.
// Use port=0 to let the OS pick an ephemeral port (useful for testing).
// The standard HiSLIP port is 4880.
func NewHiSLIPProtocol(host string, port int, subAddress string) *HiSLIPProtocol {
	if host == "" {
		host = "127.0.0.1"
	}
	if subAddress == "" {
		subAddress = "hislip0"
	}
	return &HiSLIPProtocol{
		Host:          host,
		Port:          port,
		SubAddress:    subAddress,
		sessions:      make(map[uint16]*hislipSession),
		nextSessionID: 1,
	}
}

// Attach gives the protocol access to a CommandEngine.
func (p *HiSLIPProtocol) Attach(engine *CommandEngine) {
	p.engine = engine
}

// OnTrigger registers a callback invoked when a client sends MSG_TRIGGER.
func (p *HiSLIPProtocol) OnTrigger(fn func()) {
	p.onTrigger = fn
}

// OnClear registers a callback invoked when the device-clear handshake completes.
func (p *HiSLIPProtocol) OnClear(fn func()) {
	p.onClear = fn
}

// Start begins listening for HiSLIP connections. Non-blocking.
func (p *HiSLIPProtocol) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", p.Host, p.Port))
	if err != nil {
		return err
	}
	p.listener = ln
	p.Port = ln.Addr().(*net.TCPAddr).Port
	p.running.Store(true)

	p.engine.OnSRQ(p.pushSRQ)

	go p.acceptLoop()
	log.Printf("HiSLIP server listening on %s:%d", p.Host, p.Port)
	return nil
}

// Stop closes the listener and all active sessions.
func (p *HiSLIPProtocol) Stop() {
	if !p.running.Swap(false) {
		return
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
	p.sessionsMu.Lock()
	sessions := make([]*hislipSession, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.sessions = make(map[uint16]*hislipSession)
	p.sessionsMu.Unlock()

	for _, s := range sessions {
		s.close()
	}
}

// ResourceString returns the VISA TCPIP resource string for this server.
func (p *HiSLIPProtocol) ResourceString() string {
	return fmt.Sprintf("TCPIP0::%s::%s::INSTR", p.Host, p.SubAddress)
}

// pushSRQ sends MSG_ASYNC_SERVICE_REQUEST to all sessions with an async channel.
func (p *HiSLIPProtocol) pushSRQ() {
	stb := p.engine.ReadSTB()
	p.sessionsMu.Lock()
	sessions := make([]*hislipSession, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.sessionsMu.Unlock()

	for _, s := range sessions {
		if s.isClosed() {
			continue
		}
		s.mu.Lock()
		aconn := s.asyncConn
		s.mu.Unlock()
		if aconn == nil {
			continue
		}
		s.sendMu.Lock()
		_ = sendMsg(aconn, msgAsyncServiceRequest, stb, 0, nil)
		s.sendMu.Unlock()
	}
}

func (p *HiSLIPProtocol) removeSession(id uint16) {
	p.lockMgr.releaseForSession(id)
	p.sessionsMu.Lock()
	delete(p.sessions, id)
	p.sessionsMu.Unlock()
}

// ---------------------------------------------------------------------------
// Accept loop
// ---------------------------------------------------------------------------

func (p *HiSLIPProtocol) acceptLoop() {
	for p.running.Load() {
		conn, err := p.listener.Accept()
		if err != nil {
			if p.running.Load() {
				log.Printf("HiSLIP accept error: %v", err)
			}
			return
		}
		go p.handleConnection(conn)
	}
}

func (p *HiSLIPProtocol) handleConnection(conn net.Conn) {
	msg, err := recvMsg(conn, 0)
	if err != nil {
		_ = conn.Close()
		return
	}

	switch msg.msgType {
	case msgInitialize:
		p.handleSyncInit(conn, msg)
	case msgAsyncInitialize:
		p.handleAsyncInit(conn, msg)
	default:
		log.Printf("HiSLIP: unexpected first message type %d", msg.msgType)
		sendFatalError(conn, fatalBadMsgType, "Expected Initialize or AsyncInitialize")
	}
}

// ---------------------------------------------------------------------------
// Sync-channel initialization
// ---------------------------------------------------------------------------

func (p *HiSLIPProtocol) handleSyncInit(conn net.Conn, msg *hislipMsg) {
	subAddr := strings.TrimRight(string(msg.payload), "\x00")

	if subAddr != "" && subAddr != p.SubAddress {
		log.Printf("HiSLIP sync init: wrong sub-address %q (expected %q)", subAddr, p.SubAddress)
		sendFatalError(conn, fatalUnidentified, "Unknown sub-address: "+subAddr)
		return
	}

	p.sessionsMu.Lock()
	id := p.nextSessionID
	p.nextSessionID++
	sess := newSession(id)
	sess.syncConn = conn
	p.sessions[id] = sess
	p.sessionsMu.Unlock()

	clientVersion := uint16(msg.msgParam >> 16)
	serverVersion := uint16(0x0100)
	var negotiated uint16
	if clientVersion > 0 {
		negotiated = serverVersion
		if clientVersion < serverVersion {
			negotiated = clientVersion
		}
	} else {
		negotiated = serverVersion
	}

	respParam := (uint32(negotiated) << 16) | uint32(id)
	if err := sendMsg(conn, msgInitializeResponse, 0, respParam, nil); err != nil {
		p.removeSession(id)
		_ = conn.Close()
		return
	}

	log.Printf("HiSLIP session %d: sync init (version=0x%04X)", id, negotiated)
	p.syncLoop(conn, sess)
	p.removeSession(id)
}

// ---------------------------------------------------------------------------
// Async-channel initialization
// ---------------------------------------------------------------------------

func (p *HiSLIPProtocol) handleAsyncInit(conn net.Conn, msg *hislipMsg) {
	sessID := uint16(msg.msgParam & 0xFFFF)

	p.sessionsMu.Lock()
	sess := p.sessions[sessID]
	p.sessionsMu.Unlock()

	if sess == nil {
		log.Printf("HiSLIP async init: unknown session %d", sessID)
		sendFatalError(conn, fatalUnidentified, "Unknown session ID")
		return
	}

	sess.mu.Lock()
	sess.asyncConn = conn
	sess.mu.Unlock()

	if err := sendMsg(conn, msgAsyncInitializeResponse, 0, VendorID, nil); err != nil {
		return
	}

	log.Printf("HiSLIP session %d: async init", sessID)
	p.asyncLoop(conn, sess)

	// Async connection closed; clear it so pushSRQ skips this session.
	sess.mu.Lock()
	sess.asyncConn = nil
	sess.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Sync message loop
// ---------------------------------------------------------------------------

func (p *HiSLIPProtocol) syncLoop(conn net.Conn, sess *hislipSession) {
	for p.running.Load() {
		sess.mu.Lock()
		maxSize := sess.maxMsgSize
		sess.mu.Unlock()

		msg, err := recvMsg(conn, maxSize)
		if err != nil {
			if err != io.EOF && p.running.Load() {
				log.Printf("HiSLIP session %d sync closed: %v", sess.id, err)
			}
			break
		}

		switch msg.msgType {
		case msgData:
			sess.mu.Lock()
			sess.writeBuf = append(sess.writeBuf, msg.payload...)
			sess.mu.Unlock()

		case msgDataEnd:
			sess.mu.Lock()
			sess.writeBuf = append(sess.writeBuf, msg.payload...)
			sess.messageID = msg.msgParam
			msgID := msg.msgParam
			data := strings.TrimSpace(string(sess.writeBuf))
			sess.writeBuf = sess.writeBuf[:0]
			sess.inProgress = &msgID
			sess.mu.Unlock()

			response, hasResp := safeProcessCommand(p.engine, data)

			sess.mu.Lock()
			sess.inProgress = nil
			sess.mu.Unlock()

			if hasResp {
				payload := []byte(response + "\n")
				sess.sendMu.Lock()
				_ = sendMsg(conn, msgDataEnd, 0, msgID, payload)
				sess.sendMu.Unlock()
				sess.mu.Lock()
				sess.mav = true
				sess.mu.Unlock()
			}

		case msgTrigger:
			sess.mu.Lock()
			sess.messageID = msg.msgParam
			sess.mu.Unlock()
			if p.onTrigger != nil {
				p.onTrigger()
			}

		case msgDeviceClearComplete:
			p.engine.ProcessCommand("*CLS")
			if p.onClear != nil {
				p.onClear()
			}
			sess.mu.Lock()
			sess.messageID = 0xFFFFFF00
			sess.writeBuf = sess.writeBuf[:0]
			sess.inProgress = nil
			sess.clearReq = false
			aconn := sess.asyncConn
			sess.mu.Unlock()

			if aconn != nil {
				sess.sendMu.Lock()
				_ = sendMsg(aconn, msgAsyncInterrupted, 0, sess.messageID, nil)
				sess.sendMu.Unlock()
			}
			sess.sendMu.Lock()
			_ = sendMsg(conn, msgDeviceClearAck, 0, 0, nil)
			sess.sendMu.Unlock()

		default:
			log.Printf("HiSLIP session %d: unhandled sync message type %d", sess.id, msg.msgType)
		}
	}
}

// safeProcessCommand wraps ProcessCommand with panic recovery, matching the
// Python SDK's behavior of returning an error string rather than crashing.
func safeProcessCommand(engine *CommandEngine, data string) (response string, hasResp bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("engine panic processing %q: %v", data, r)
			response = `-100,"Command error"`
			hasResp = true
		}
	}()
	return engine.ProcessCommand(data)
}

// ---------------------------------------------------------------------------
// Async message loop
// ---------------------------------------------------------------------------

func (p *HiSLIPProtocol) asyncLoop(conn net.Conn, sess *hislipSession) {
	for p.running.Load() {
		sess.mu.Lock()
		maxSize := sess.maxMsgSize
		sess.mu.Unlock()

		msg, err := recvMsg(conn, maxSize)
		if err != nil {
			if err != io.EOF && p.running.Load() {
				log.Printf("HiSLIP session %d async closed: %v", sess.id, err)
			}
			break
		}

		switch msg.msgType {
		case msgAsyncMaxMsgSize:
			if len(msg.payload) >= 8 {
				proposed := binary.BigEndian.Uint64(msg.payload[:8])
				negotiated := proposed
				if negotiated > defaultMaxMsgSize {
					negotiated = defaultMaxMsgSize
				}
				sess.mu.Lock()
				sess.maxMsgSize = negotiated
				sess.mu.Unlock()
				resp := make([]byte, 8)
				binary.BigEndian.PutUint64(resp, negotiated)
				sess.sendMu.Lock()
				_ = sendMsg(conn, msgAsyncMaxMsgSizeResp, 0, 0, resp)
				sess.sendMu.Unlock()
			}

		case msgAsyncLock:
			// control_code: 0=release, 1=exclusive
			var respCode uint8
			if msg.controlCode == 0 {
				respCode = p.lockMgr.release(sess.id)
			} else {
				respCode = p.lockMgr.requestExclusive(sess.id, msg.msgParam)
			}
			sess.sendMu.Lock()
			_ = sendMsg(conn, msgAsyncLockResponse, respCode, 0, nil)
			sess.sendMu.Unlock()

		case msgAsyncStatusQuery:
			rmt := msg.controlCode & 1
			sess.mu.Lock()
			if rmt != 0 {
				sess.mav = false
			}
			mav := sess.mav
			sess.mu.Unlock()

			stb := p.engine.ReadSTB()
			if mav {
				stb |= 0x10 // MAV bit
			}
			sess.sendMu.Lock()
			_ = sendMsg(conn, msgAsyncStatusResponse, stb, 0, nil)
			sess.sendMu.Unlock()

		case msgAsyncDeviceClear:
			sess.mu.Lock()
			sess.clearReq = true
			inProg := sess.inProgress
			sess.mu.Unlock()

			sess.sendMu.Lock()
			_ = sendMsg(conn, msgAsyncDeviceClearAck, 0, 0, nil)
			sess.sendMu.Unlock()

			if inProg != nil {
				sess.mu.Lock()
				sconn := sess.syncConn
				sess.mu.Unlock()
				if sconn != nil {
					sess.sendMu.Lock()
					_ = sendMsg(sconn, msgInterrupted, 0, *inProg, nil)
					sess.sendMu.Unlock()
				}
			}

		case msgAsyncRemoteLocalControl:
			sess.sendMu.Lock()
			_ = sendMsg(conn, msgAsyncRemoteLocalResp, 0, 0, nil)
			sess.sendMu.Unlock()

		default:
			log.Printf("HiSLIP session %d: unhandled async message type %d", sess.id, msg.msgType)
		}
	}
}
