package kvm

import (
	"bytes"
	"context"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// The camera WebSocket takes the browser's camera frames over TCP. The KVM's
// processor spends much less on a TCP stream than on the WebRTC data
// channel, whose transport runs in the application, so larger frames fit.
// Each binary message is one frame with the camera frame header.
const virtualVideoWebSocketPath = "/virtual-video/ws"

func handleVirtualVideoWebSocket(c *gin.Context) {
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		// The frames are JPEG or H.264: compressing them again costs time
		// and saves nothing.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		logger.Warn().Err(err).Msg("failed to accept camera websocket")
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(defaultVirtualVideoMaxFrameSize + virtualVideoHeaderSize)
	if virtualVideo == nil {
		return
	}
	id := virtualVideo.BeginChannel()
	defer virtualVideo.StopChannel(id)
	ctx := context.Background()
	// One buffer for the connection: the handler copies the frame before it
	// returns.
	var frame bytes.Buffer
	for {
		kind, reader, err := conn.Reader(ctx)
		if err != nil {
			return
		}
		frame.Reset()
		if _, err := frame.ReadFrom(reader); err != nil {
			return
		}
		if kind != websocket.MessageBinary {
			continue
		}
		if err := virtualVideo.HandleChannelFrameData(id, frame.Bytes(), "websocket"); err != nil {
			logger.Warn().Err(err).Msg("failed to handle camera frame")
		}
	}
}
