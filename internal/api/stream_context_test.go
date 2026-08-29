package api

import (
	"context"
	"testing"
	"time"
)

func TestRequestStreamContextEndsForClientOrApplicationShutdown(t *testing.T) {
	t.Run("application shutdown", func(t *testing.T) {
		shutdownCtx, shutdown := context.WithCancel(context.Background())
		ctx, cancel := requestStreamContext(context.Background(), shutdownCtx)
		defer cancel()

		shutdown()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("stream context stayed open after application shutdown")
		}
	})

	t.Run("client disconnect", func(t *testing.T) {
		requestCtx, disconnect := context.WithCancel(context.Background())
		ctx, cancel := requestStreamContext(requestCtx, context.Background())
		defer cancel()

		disconnect()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("stream context stayed open after client disconnect")
		}
	})
}
