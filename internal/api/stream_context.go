package api

import "context"

// requestStreamContext ends a long-lived HTTP/WebSocket handler when either
// its client disconnects or the application starts shutting down. net/http's
// graceful Shutdown waits for streams, so they need an explicit application
// lifecycle signal instead of relying on the request context alone.
func requestStreamContext(requestCtx, shutdownCtx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(requestCtx)
	if shutdownCtx == nil {
		return ctx, cancel
	}

	go func() {
		select {
		case <-shutdownCtx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
