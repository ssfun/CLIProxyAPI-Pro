package pluginhost

import (
	"context"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func pluginExecutorUsageContext(ctx context.Context, headers http.Header) context.Context {
	logging.SetResponseHeaders(ctx, headers)
	if len(headers) == 0 || len(logging.GetResponseHeaders(ctx)) > 0 {
		return ctx
	}
	ctx = logging.WithResponseHeadersHolder(ctx)
	logging.SetResponseHeaders(ctx, headers)
	return ctx
}

func publishPluginExecutorUsage(ctx context.Context, reporter *helps.UsageReporter, format sdktranslator.Format, payload []byte, stream bool) bool {
	var buffer helps.StreamUsageBuffer
	observePluginExecutorUsage(&buffer, format, payload, stream)
	return buffer.Publish(ctx, reporter)
}

func pluginExecutorUsageFormat(prepared preparedExecutorCall) sdktranslator.Format {
	if prepared.requestedFormat != "" {
		return prepared.requestedFormat
	}
	return prepared.outputFormat
}

func observePluginExecutorUsage(buffer *helps.StreamUsageBuffer, format sdktranslator.Format, payload []byte, stream bool) {
	if buffer == nil || len(payload) == 0 {
		return
	}
	// Upstream owns protocol parsing, structured usage, and stream merging. Keep
	// publication in the host so translation and terminal outcome settle first.
	if stream {
		helps.ObservePluginExecutorStreamUsage(format.String(), payload, buffer)
		return
	}
	buffer.Observe(helps.ParsePluginExecutorResponseUsage(format.String(), payload), true)
}

type pluginExecutorEmptyStreamError struct{}

func (pluginExecutorEmptyStreamError) Error() string {
	return "plugin executor stream closed before first payload"
}
func (pluginExecutorEmptyStreamError) StatusCode() int { return http.StatusBadGateway }

// Non-streaming plugin responses are parsed before host translation. The
// reporter's format was configured from prepared.outputFormat at dispatch.
func observePluginResponseModel(reporter *helps.UsageReporter, payload []byte) {
	if reporter == nil {
		return
	}
	reporter.ObserveResponseModel(payload)
}
