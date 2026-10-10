package handlers

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"net/http/httptest"
	"testing"

	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestNextStreamChunkPrefersCancellationOverClosedChannel(t *testing.T) {
	for i := 0; i < 1000; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		chunks := make(chan coreexecutor.StreamChunk)
		cancel()
		close(chunks)

		_, ok, canceled := nextStreamChunk(ctx, nil, nil, chunks)
		if ok || !canceled {
			t.Fatalf("iteration %d: nextStreamChunk() = (_, %v, %v), want (_, false, true)", i, ok, canceled)
		}
	}
}

// Failure cases: canceled EOF must not synthesize an incomplete-response error;
// queued upstream errors must survive EOF; a live EOF must still be validated.
func TestForwardStreamCancellationAtEOF(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		canceled, upstreamError bool
	}{
		{"canceled_eof", true, false}, {"upstream_error", false, true}, {"live_eof", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				ctx, stop := context.WithCancel(context.Background())
				if tc.canceled {
					stop()
				}
				data := make(chan []byte)
				close(data)
				errs := make(chan *interfaces.ErrorMessage, 1)
				upstream := errors.New("upstream failed")
				if tc.upstreamError {
					errs <- &interfaces.ErrorMessage{Error: upstream}
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("GET", "/", nil).WithContext(ctx)
				missing := errors.New("missing terminal event")
				var got error
				h := &BaseAPIHandler{Cfg: &config.SDKConfig{}}
				h.ForwardStream(c, recorder, func(err error) { got = err }, data, errs, StreamForwardOptions{
					CloseError: func() *interfaces.ErrorMessage { return &interfaces.ErrorMessage{Error: missing} },
				})
				stop()
				want := missing
				if tc.canceled {
					want = context.Canceled
				} else if tc.upstreamError {
					want = upstream
				}
				if !errors.Is(got, want) {
					t.Fatalf("iteration %d: got %v, want %v", i, got, want)
				}
			}
		})
	}
}
