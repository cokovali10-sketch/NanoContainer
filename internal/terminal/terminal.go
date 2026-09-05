//go:build linux

package terminal

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

type WebTerminal struct {
	cfg      *config.Config
	sessions map[string]*TerminalSession
	mu       sync.RWMutex
}

type TerminalSession struct {
	ID          string   `json:"id"`
	ContainerID string   `json:"container_id"`
	Cmd         *exec.Cmd
	PTY         *os.File
	Width       int      `json:"width"`
	Height      int      `json:"height"`
	CreatedAt   int64    `json:"created_at"`
	createdAt   time.Time
	connected   bool
	mu          sync.Mutex
}

type TerminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type wsResponse struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func NewWebTerminal(cfg *config.Config) *WebTerminal {
	return &WebTerminal{
		cfg:      cfg,
		sessions: make(map[string]*TerminalSession),
	}
}

func (wt *WebTerminal) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	containerID := r.URL.Query().Get("container")
	if containerID == "" {
		http.Error(w, "container parameter required", http.StatusBadRequest)
		return
	}

	stateStore := store.New(wt.cfg.ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		http.Error(w, fmt.Sprintf("container not found: %v", err), http.StatusNotFound)
		return
	}
	if state.Status != store.StatusRunning {
		http.Error(w, "container is not running", http.StatusBadRequest)
		return
	}

	ws, err := upgradeHTTPWebSocket(w, r)
	if err != nil {
		return
	}

	width := parseIntParam(r, "cols", 80)
	height := parseIntParam(r, "rows", 24)

	session, err := wt.CreateSession(containerID, width, height)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	defer func() {
		wt.DestroySession(session.ID)
		ws.Close()
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := session.PTY.Read(buf)
			if readErr != nil {
				session.mu.Lock()
				session.connected = false
				session.mu.Unlock()
				return
			}
			msg := wsResponse{
				Type: "output",
				Data: string(buf[:n]),
			}
			data, _ := json.Marshal(msg)
			ws.WriteMessage(textMessage, data)
		}
	}()

	for {
		raw, readErr := ws.ReadMessage()
		if readErr != nil {
			break
		}

		var msg TerminalMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "input":
			if session.PTY != nil {
				session.PTY.Write([]byte(msg.Data)) //nolint:errcheck
			}
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				wt.ResizeSession(session.ID, msg.Cols, msg.Rows)
			}
		case "exit":
			return
		}
	}
}

func (wt *WebTerminal) CreateSession(containerID string, width, height int) (*TerminalSession, error) {
	stateStore := store.New(wt.cfg.ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		return nil, fmt.Errorf("load container state: %w", err)
	}
	if state.Status != store.StatusRunning {
		return nil, fmt.Errorf("container %s is not running", containerID)
	}

	ptyMaster, ptySlave, err := openPTY()
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}

	resizePTY(ptyMaster, width, height)

	nsenterArgs := []string{
		"--target", strconv.Itoa(state.PID),
		"--pid",
		"--mount",
		"--net",
		"--uts",
		"--",
		"/bin/sh",
	}

	cmd := exec.Command("nsenter", nsenterArgs...)
	cmd.Stdin = ptySlave
	cmd.Stdout = ptySlave
	cmd.Stderr = ptySlave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	if err := cmd.Start(); err != nil {
		ptyMaster.Close()
		ptySlave.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}
	ptySlave.Close()

	sessionID := generateSessionID()
	now := time.Now()
	session := &TerminalSession{
		ID:          sessionID,
		ContainerID: containerID,
		Cmd:         cmd,
		PTY:         ptyMaster,
		Width:       width,
		Height:      height,
		CreatedAt:   now.Unix(),
		createdAt:   now,
		connected:   true,
	}

	go func() {
		_ = cmd.Wait()
		session.mu.Lock()
		session.connected = false
		session.mu.Unlock()
	}()

	wt.mu.Lock()
	wt.sessions[sessionID] = session
	wt.mu.Unlock()

	return session, nil
}

func (wt *WebTerminal) DestroySession(sessionID string) error {
	wt.mu.Lock()
	session, ok := wt.sessions[sessionID]
	if !ok {
		wt.mu.Unlock()
		return fmt.Errorf("session %s not found", sessionID)
	}
	delete(wt.sessions, sessionID)
	wt.mu.Unlock()

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.PTY != nil {
		session.PTY.Close()
	}
	if session.Cmd != nil && session.Cmd.Process != nil {
		session.Cmd.Process.Signal(syscall.SIGTERM) //nolint:errcheck
		time.Sleep(100 * time.Millisecond)
		session.Cmd.Process.Signal(syscall.SIGKILL) //nolint:errcheck
	}
	session.connected = false
	return nil
}

func (wt *WebTerminal) ListSessions() []*TerminalSession {
	wt.mu.RLock()
	defer wt.mu.RUnlock()

	sessions := make([]*TerminalSession, 0, len(wt.sessions))
	for _, s := range wt.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

func (wt *WebTerminal) ResizeSession(sessionID string, width, height int) error {
	wt.mu.RLock()
	session, ok := wt.sessions[sessionID]
	wt.mu.RUnlock()

	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	if width < 1 || width > 1024 || height < 1 || height > 512 {
		return fmt.Errorf("invalid dimensions: %dx%d", width, height)
	}

	if err := resizePTY(session.PTY, width, height); err != nil {
		return fmt.Errorf("resize pty: %w", err)
	}

	session.mu.Lock()
	session.Width = width
	session.Height = height
	session.mu.Unlock()

	return nil
}

func (wt *WebTerminal) ServeTerminalUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(TerminalUI)) //nolint:errcheck
}

func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open ptmx: %w", err)
	}

	var unlockInt int
	if err := ptioctl(master.Fd(), syscall.TIOCSPTLCK, &unlockInt); err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("unlock pty slave: %w", err)
	}

	var ptyNumber int
	if err := ptioctl(master.Fd(), syscall.TIOCGPTN, &ptyNumber); err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("get pty number: %w", err)
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", ptyNumber)
	slave, err = os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("open slave pty %s: %w", slavePath, err)
	}

	return master, slave, nil
}

func resizePTY(f *os.File, cols, rows int) error {
	ws := winsize{
		Row: uint16(rows),
		Col: uint16(cols),
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return errno
	}
	return nil
}

type winsize struct {
	Row uint16
	Col uint16
	X   uint16
	Y   uint16
}

func ptioctl(fd uintptr, request uintptr, arg *int) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(arg)))
	if errno != 0 {
		return errno
	}
	return nil
}

const (
	textMessage   = 0x01
	binaryMessage = 0x02
	closeMessage  = 0x08
	pingMessage   = 0x09
	pongMessage   = 0x0A
	maxFrameSize  = 65536
)

type wsFrame struct {
	fin        bool
	opcode     byte
	masked     bool
	maskKey    [4]byte
	payloadLen uint64
	payload    []byte
}

type httpWSConn struct {
	conn     net.Conn
	reader   io.Reader
	mu       sync.Mutex
	isClosed bool
}

func upgradeHTTPWebSocket(w http.ResponseWriter, r *http.Request) (*httpWSConn, error) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "server does not support hijacking", http.StatusInternalServerError)
		return nil, fmt.Errorf("hijacking not supported")
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, fmt.Errorf("missing Sec-WebSocket-Key")
	}

	acceptKey := computeAcceptKey(key)
	responseHeaders := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey + "\r\n" +
		"\r\n"

	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijack: %w", err)
	}

	if _, err := conn.Write([]byte(responseHeaders)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write handshake: %w", err)
	}

	var reader io.Reader = conn
	if buf.Writer.Buffered() > 0 {
		remaining, _ := io.ReadAll(buf.Reader)
		reader = io.MultiReader(bytes.NewReader(remaining), conn)
	}

	return &httpWSConn{
		conn:   conn,
		reader: reader,
	}, nil
}

func (c *httpWSConn) ReadMessage() ([]byte, error) {
	for {
		frame, err := c.readFrame()
		if err != nil {
			return nil, err
		}

		switch frame.opcode {
		case closeMessage:
			return nil, io.ErrClosedPipe
		case pingMessage:
			c.writeFrame(wsFrame{
				fin:     true,
				opcode:  pongMessage,
				payload: frame.payload,
			})
			continue
		case pongMessage:
			continue
		case textMessage, binaryMessage:
			return frame.payload, nil
		}
	}
}

func (c *httpWSConn) WriteMessage(messageType byte, data []byte) error {
	return c.writeFrame(wsFrame{
		fin:     true,
		opcode:  messageType,
		payload: data,
	})
}

func (c *httpWSConn) writeFrame(frame wsFrame) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var header []byte
	payloadLen := len(frame.payload)

	header = append(header, 0x80|frame.opcode)

	if payloadLen < 126 {
		header = append(header, byte(payloadLen))
	} else if payloadLen <= 65535 {
		header = append(header, 126)
		header = append(header, byte(payloadLen>>8), byte(payloadLen))
	} else {
		header = append(header, 127)
		for i := 56; i >= 0; i -= 8 {
			header = append(header, byte(payloadLen>>uint(i)))
		}
	}

	header = append(header, frame.payload...)

	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	_, err := c.conn.Write(header)
	return err
}

func (c *httpWSConn) readFrame() (*wsFrame, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return nil, err
	}

	frame := &wsFrame{
		fin:    (header[0] & 0x80) != 0,
		opcode: header[0] & 0x0F,
		masked: (header[1] & 0x80) != 0,
	}

	payloadLen := uint64(header[1] & 0x7F)

	switch payloadLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, ext); err != nil {
			return nil, err
		}
		payloadLen = uint64(ext[0])<<8 | uint64(ext[1])
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.reader, ext); err != nil {
			return nil, err
		}
		payloadLen = 0
		for i := 0; i < 8; i++ {
			payloadLen = payloadLen<<8 | uint64(ext[i])
		}
	}

	frame.payloadLen = payloadLen

	if frame.masked {
		if _, err := io.ReadFull(c.reader, frame.maskKey[:]); err != nil {
			return nil, err
		}
	}

	if payloadLen > maxFrameSize {
		return nil, fmt.Errorf("frame too large: %d bytes", payloadLen)
	}

	if payloadLen > 0 {
		frame.payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(c.reader, frame.payload); err != nil {
			return nil, err
		}

		if frame.masked {
			for i := uint64(0); i < payloadLen; i++ {
				frame.payload[i] ^= frame.maskKey[i%4]
			}
		}
	}

	return frame, nil
}

func (c *httpWSConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed {
		return nil
	}
	c.isClosed = true

	closeFrame := wsFrame{
		fin:     true,
		opcode:  closeMessage,
		payload: []byte{0x03, 0xE8},
	}
	c.writeFrame(closeFrame)

	return c.conn.Close()
}

func computeAcceptKey(key string) string {
	const magicGUID = "258EAFA5-E914-47DA-95CA-5AB9FC16B654"
	h := sha1.New()
	h.Write([]byte(key + magicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (wt *WebTerminal) CleanupDisconnected() int {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	count := 0
	for id, session := range wt.sessions {
		session.mu.Lock()
		connected := session.connected
		session.mu.Unlock()

		if !connected {
			if session.PTY != nil {
				session.PTY.Close()
			}
			delete(wt.sessions, id)
			count++
		}
	}
	return count
}

func (wt *WebTerminal) GetSessionContainerPaths() map[string]string {
	wt.mu.RLock()
	defer wt.mu.RUnlock()

	result := make(map[string]string)
	for _, s := range wt.sessions {
		containerDir := filepath.Join(wt.cfg.ContainerDir, s.ContainerID)
		result[s.ID] = containerDir
	}
	return result
}

func (wt *WebTerminal) KillSession(sessionID string, sig syscall.Signal) error {
	wt.mu.RLock()
	session, ok := wt.sessions[sessionID]
	wt.mu.RUnlock()

	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	if session.Cmd != nil && session.Cmd.Process != nil {
		return session.Cmd.Process.Signal(sig)
	}
	return nil
}

func parseIntParam(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func generateSessionID() string {
	const chars = "abcdef0123456789"
	b := make([]byte, 16)
	for i := range b {
		b[i] = chars[time.Now().UnixNano()%int64(len(chars))]
		time.Sleep(1)
	}
	return string(b)
}

func getStoreContainerDir(cfg *config.Config, containerID string) string {
	return filepath.Join(cfg.ContainerDir, containerID)
}
