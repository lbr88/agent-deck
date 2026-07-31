package hub

import (
	"time"

	"github.com/gorilla/websocket"
)

const (
	hubWriteTimeout        = 5 * time.Second
	defaultHubPingInterval = 30 * time.Second
	defaultHubPongWait     = 45 * time.Second
)

func configureWebSocketReadLiveness(conn *websocket.Conn, pongWait time.Duration) error {
	if conn == nil {
		return nil
	}
	if pongWait <= 0 {
		pongWait = defaultHubPongWait
	}
	refresh := func() error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	}
	if err := refresh(); err != nil {
		return err
	}
	conn.SetPongHandler(func(string) error {
		return refresh()
	})
	return nil
}

func writeWebSocketPing(conn *websocket.Conn) error {
	if conn == nil {
		return nil
	}
	return conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(hubWriteTimeout))
}

func writeWebSocketJSON(conn *websocket.Conn, v any) error {
	if conn == nil {
		return nil
	}
	_ = conn.SetWriteDeadline(time.Now().Add(hubWriteTimeout))
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	return conn.WriteJSON(v)
}
