package hub

import (
	"time"

	"github.com/gorilla/websocket"
)

const hubWriteTimeout = 5 * time.Second

func writeWebSocketJSON(conn *websocket.Conn, v any) error {
	if conn == nil {
		return nil
	}
	_ = conn.SetWriteDeadline(time.Now().Add(hubWriteTimeout))
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	return conn.WriteJSON(v)
}
