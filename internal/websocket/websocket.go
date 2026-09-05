//go:build linux

package websocket

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 64 * 1024
	frameHeaderSize = 4
)

type MessageType int

const (
	TextMessage   MessageType = 1
	BinaryMessage MessageType = 2
	PingMessage   MessageType = 9
	PongMessage   MessageType = 10
)

type Message struct {
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
}

type Frame struct {
	Length  uint32
	Payload []byte
}

type Client struct {
	conn   net.Conn
	id     string
	send   chan []byte
	hub    *Hub
	closed bool
	mu     sync.Mutex
}

type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	done       chan struct{}
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		done:       make(chan struct{}),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					h.mu.RUnlock()
					h.mu.Lock()
					delete(h.clients, client)
					close(client.send)
					h.mu.Unlock()
					h.mu.RLock()
				}
			}
			h.mu.RUnlock()

		case <-h.done:
			return
		}
	}
}

func (h *Hub) Broadcast(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.broadcast <- data
}

func (h *Hub) BroadcastJSON(data interface{}) {
	msg := Message{
		Type:      "json",
		Data:      data,
		Timestamp: time.Now(),
	}
	h.Broadcast(msg)
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgradeConnection(w, r)
	if err != nil {
		return
	}

	client := &Client{
		conn: conn,
		id:   generateClientID(),
		send: make(chan []byte, 256),
		hub:  h,
	}

	h.register <- client

	go client.WritePump()
	go client.ReadPump()
}

func (c *Client) ReadPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetReadLimit(maxMessageSize)

	for {
		frame, err := readFrame(c.conn)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return
			}
			if err == io.EOF {
				return
			}
			return
		}

		switch MessageType(frame.Payload[0]) {
		case PingMessage:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			pongFrame := Frame{
				Length:  1,
				Payload: []byte{byte(PongMessage)},
			}
			writeFrame(c.conn, pongFrame)

		case PongMessage:
			c.conn.SetReadDeadline(time.Now().Add(pongWait))

		case TextMessage, BinaryMessage:
			c.hub.broadcast <- frame.Payload[1:]
		}
	}
}

func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.Write([]byte{byte(8), 0, 0, 0})
				return
			}

			w := c.conn.(io.Writer)
			header := make([]byte, frameHeaderSize)
			binary.BigEndian.PutUint32(header, uint32(len(message)+1))
			w.Write(header)
			w.Write([]byte{byte(TextMessage)})
			w.Write(message)

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			pingFrame := Frame{
				Length:  1,
				Payload: []byte{byte(PingMessage)},
			}
			writeFrame(c.conn, pingFrame)
		}
	}
}

func (c *Client) Send(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	select {
	case c.send <- data:
	default:
		c.Close()
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	c.closed = true
	c.conn.Close()
}

func readFrame(r io.Reader) (*Frame, error) {
	header := make([]byte, frameHeaderSize)
	_, err := io.ReadFull(r, header)
	if err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header)
	if length > maxMessageSize+1 {
		return nil, fmt.Errorf("frame too large: %d bytes", length)
	}

	payload := make([]byte, length)
	_, err = io.ReadFull(r, payload)
	if err != nil {
		return nil, err
	}

	return &Frame{
		Length:  length,
		Payload: payload,
	}, nil
}

func writeFrame(w io.Writer, frame Frame) error {
	header := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(header, frame.Length)

	_, err := w.Write(header)
	if err != nil {
		return err
	}

	_, err = w.Write(frame.Payload)
	return err
}

func upgradeConnection(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "server does not support hijacking", http.StatusInternalServerError)
		return nil, fmt.Errorf("hijacking not supported")
	}

	w.WriteHeader(http.StatusSwitchingProtocols)

	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}

	if buf.Writer.Buffered() > 0 {
		conn.Close()
		return nil, fmt.Errorf("buffered data before handshake")
	}

	return conn, nil
}

func generateClientID() string {
	return fmt.Sprintf("client-%d", time.Now().UnixNano())
}
