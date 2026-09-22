package openai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

// Recreate the producer's terminal state before forwarding starts: the error
// is buffered and both channels are closed. Either select arm may win.
func TestForwardResponsesWebsocketPreservesQueuedErrorAtEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	for _, duplex := range []bool{false, true} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
			for i := 0; i < 100; i++ {
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
				data := make(chan []byte)
				errs := make(chan *interfaces.ErrorMessage, 1)
				want := &interfaces.ErrorMessage{StatusCode: status, Error: errors.New("credential failed")}
				errs <- want
				close(errs)
				close(data)
				var canceled interface{}
				_, _, _, got, err := h.forwardResponsesWebsocket(
					ctx, nil, func(args ...interface{}) { canceled = args[0] }, data, errs,
					newInMemoryWebsocketTimelineLog(), "terminal-order",
					responsesWebsocketForwardOptions{
						duplexStream:  func() bool { return duplex },
						suppressError: shouldReplayResponsesWebsocketPinnedAuthFailure,
					},
				)
				if got != want || err != nil || canceled != want.Error {
					t.Fatalf("duplex=%v status=%d iteration=%d: got=%v err=%v cancel=%v; want original replay error", duplex, status, i, got, err, canceled)
				}
			}
		}
	}
}

func TestForwardResponsesWebsocketEOFWithoutQueuedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	for _, duplex := range []bool{false, true} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		data := make(chan []byte)
		errs := make(chan *interfaces.ErrorMessage)
		close(data)
		close(errs)
		_, _, _, got, err := h.forwardResponsesWebsocket(
			ctx, nil, func(...interface{}) {}, data, errs,
			newInMemoryWebsocketTimelineLog(), "terminal-eof",
			responsesWebsocketForwardOptions{duplexStream: func() bool { return duplex }},
		)
		if !errors.Is(err, websocket.ErrCloseSent) {
			t.Fatalf("duplex=%v: err=%v, want close", duplex, err)
		}
		if duplex && got != nil || !duplex && (got == nil || got.StatusCode != http.StatusRequestTimeout) {
			t.Fatalf("duplex=%v: unexpected EOF error: %v", duplex, got)
		}
	}
}
