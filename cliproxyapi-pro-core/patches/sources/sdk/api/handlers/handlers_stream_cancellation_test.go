package handlers

import (
	"context"
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
