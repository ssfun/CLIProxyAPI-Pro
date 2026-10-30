package executor

import (
	"context"
	"errors"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type claudeStreamTerminal struct {
	ctx               context.Context
	cfg               *config.Config
	reporter          *helps.UsageReporter
	out               chan<- coreexecutor.StreamChunk
	statusCode        int
	fastRequest       bool
	oauthCancellation bool
}

func (t claudeStreamTerminal) publishFailure(buffer *helps.StreamUsageBuffer, err error) {
	if buffer == nil || !buffer.PublishFailure(t.ctx, t.reporter, err) {
		t.reporter.PublishFailure(t.ctx, err)
	}
}

func (t claudeStreamTerminal) publishSuccess(buffer *helps.StreamUsageBuffer) {
	if buffer != nil {
		buffer.Publish(t.ctx, t.reporter)
	}
}

func (t claudeStreamTerminal) emitCancellation(buffer *helps.StreamUsageBuffer, cause error) bool {
	cancelErr := newClaudeOAuthCancellationError(t.ctx, t.oauthCancellation, cause)
	if cancelErr != nil {
		helps.RecordAPIResponseError(t.ctx, t.cfg, cancelErr)
		t.publishFailure(buffer, cancelErr)
		select {
		case t.out <- coreexecutor.StreamChunk{Err: cancelErr}:
		default:
		}
		return true
	}
	c := cause
	if t.ctx != nil && t.ctx.Err() != nil {
		c = t.ctx.Err()
	}
	if errors.Is(c, context.Canceled) {
		helps.RecordAPIResponseError(t.ctx, t.cfg, c)
		t.publishFailure(buffer, c)
		return true
	}
	return false
}

func (t claudeStreamTerminal) publishCancellation(buffer *helps.StreamUsageBuffer, cause error) {
	if !t.emitCancellation(buffer, cause) {
		t.publishFailure(buffer, cause)
	}
}

func (t claudeStreamTerminal) emitResponseError(buffer *helps.StreamUsageBuffer, errResponse error) {
	errResponse = wrapClaudeFastRequestError(t.fastRequest, t.statusCode, errResponse)
	helps.RecordAPIResponseError(t.ctx, t.cfg, errResponse)
	t.publishFailure(buffer, errResponse)
	select {
	case t.out <- coreexecutor.StreamChunk{Err: errResponse}:
	case <-t.ctx.Done():
	}
}

func (t claudeStreamTerminal) finishScanner(buffer *helps.StreamUsageBuffer, scannerErr error) bool {
	if t.emitCancellation(buffer, scannerErr) {
		return true
	}
	if scannerErr == nil {
		if t.ctx.Err() == nil {
			return false
		}
		t.publishFailure(buffer, t.ctx.Err())
		return true
	}
	t.emitResponseError(buffer, scannerErr)
	return true
}
