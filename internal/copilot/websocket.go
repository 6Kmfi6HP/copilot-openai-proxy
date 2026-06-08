package copilot

import (
	"time"

	"github.com/gorilla/websocket"
)

// writeControlClose sends a WebSocket close frame.
func writeControlClose(conn *websocket.Conn) error {
	return conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now(),
	)
}