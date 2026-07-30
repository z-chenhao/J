package main

import (
	"context"
	"time"
)

func contextBackground() context.Context {
	return context.Background()
}

func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}
