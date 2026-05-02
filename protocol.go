package hislip

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
)

// HiSLIP message type constants (IVI-6.1 Table 4).
const (
	msgInitialize              uint8 = 0
	msgInitializeResponse      uint8 = 1
	msgFatalError              uint8 = 2
	msgError                   uint8 = 3
	msgAsyncLock               uint8 = 4
	msgAsyncLockResponse       uint8 = 5
	msgData                    uint8 = 6
	msgDataEnd                 uint8 = 7
	msgDeviceClearComplete     uint8 = 8
	msgDeviceClearAck          uint8 = 9
	msgAsyncRemoteLocalControl uint8 = 10
	msgAsyncRemoteLocalResp    uint8 = 11
	msgTrigger                 uint8 = 12
	msgInterrupted             uint8 = 13
	msgAsyncInterrupted        uint8 = 14
	msgAsyncMaxMsgSize         uint8 = 15
	msgAsyncMaxMsgSizeResp     uint8 = 16
	msgAsyncInitialize         uint8 = 17
	msgAsyncInitializeResponse uint8 = 18
	msgAsyncDeviceClear        uint8 = 19
	msgAsyncServiceRequest     uint8 = 20
	msgAsyncStatusQuery        uint8 = 21
	msgAsyncStatusResponse     uint8 = 22
	msgAsyncDeviceClearAck     uint8 = 23
)

// HiSLIP FatalError codes (IVI-6.1 Table 5).
const (
	fatalUnidentified       uint8 = 0
	fatalBadMsgType         uint8 = 1
	fatalInitNotFirst       uint8 = 2
	fatalMaxClients         uint8 = 3
	fatalSecureNotSupported uint8 = 4
)

// VendorID is "CB" for Carbon (0x43 = 'C', 0x42 = 'B').
const VendorID uint32 = 0x00004342

const headerSize = 16
const defaultMaxMsgSize uint64 = 1 << 20 // 1 MB

// hislipMsg holds a decoded HiSLIP message.
type hislipMsg struct {
	msgType     uint8
	controlCode uint8
	msgParam    uint32
	payload     []byte
}

// sendMsg serializes and sends a HiSLIP message on conn.
func sendMsg(conn net.Conn, msgType, controlCode uint8, msgParam uint32, payload []byte) error {
	buf := make([]byte, headerSize+len(payload))
	buf[0] = 'H'
	buf[1] = 'S'
	buf[2] = msgType
	buf[3] = controlCode
	binary.BigEndian.PutUint32(buf[4:], msgParam)
	binary.BigEndian.PutUint64(buf[8:], uint64(len(payload)))
	copy(buf[headerSize:], payload)
	_, err := conn.Write(buf)
	return err
}

// recvMsg reads one HiSLIP message from conn.
// Returns nil if the connection is closed cleanly.
func recvMsg(conn net.Conn, maxPayload uint64) (*hislipMsg, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	if header[0] != 'H' || header[1] != 'S' {
		return nil, io.ErrUnexpectedEOF
	}
	payloadLen := binary.BigEndian.Uint64(header[8:])
	if maxPayload > 0 && payloadLen > maxPayload {
		return nil, io.ErrUnexpectedEOF
	}
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(conn, payload); err != nil {
			return nil, err
		}
	}
	return &hislipMsg{
		msgType:     header[2],
		controlCode: header[3],
		msgParam:    binary.BigEndian.Uint32(header[4:]),
		payload:     payload,
	}, nil
}

// sendFatalError sends a FatalError message and closes the connection.
func sendFatalError(conn net.Conn, code uint8, message string) {
	payload := []byte(message)
	_ = sendMsg(conn, msgFatalError, code, 0, payload)
	_ = conn.Close()
}

// ---------------------------------------------------------------------------
// Lock manager
// ---------------------------------------------------------------------------

type lockManager struct {
	mu     sync.Mutex
	holder *uint16 // session ID holding exclusive lock, nil if unlocked
}

// requestExclusive tries to acquire the exclusive lock for sessionID.
// Returns: 1=success, 3=error (cannot grant, timeout=0), 0=fail (would need wait).
func (lm *lockManager) requestExclusive(sessionID uint16, timeoutMS uint32) uint8 {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.holder == nil {
		id := sessionID
		lm.holder = &id
		return 1
	}
	if *lm.holder == sessionID {
		return 1
	}
	if timeoutMS == 0 {
		return 3
	}
	return 0
}

// release releases the lock held by sessionID.
// Returns 1 if the lock was held and released, 0 otherwise.
func (lm *lockManager) release(sessionID uint16) uint8 {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.holder != nil && *lm.holder == sessionID {
		lm.holder = nil
		return 1
	}
	return 0
}

// releaseForSession releases any lock held by a disconnecting session.
func (lm *lockManager) releaseForSession(sessionID uint16) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.holder != nil && *lm.holder == sessionID {
		lm.holder = nil
	}
}

// ---------------------------------------------------------------------------
// Session
// ---------------------------------------------------------------------------

type hislipSession struct {
	id        uint16
	syncConn  net.Conn
	asyncConn net.Conn

	mu         sync.Mutex
	sendMu     sync.Mutex // prevents interleaved writes across goroutines
	writeBuf   []byte
	messageID  uint32
	maxMsgSize uint64
	mav        bool
	inProgress *uint32 // message ID currently being processed
	clearReq   bool
	closed     bool
}

func newSession(id uint16) *hislipSession {
	return &hislipSession{
		id:         id,
		messageID:  0xFFFFFF00,
		maxMsgSize: defaultMaxMsgSize,
	}
}

func (s *hislipSession) close() {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if already {
		return
	}
	for _, conn := range []net.Conn{s.syncConn, s.asyncConn} {
		if conn != nil {
			_ = conn.Close()
		}
	}
}

func (s *hislipSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
