// Package inspection contains account-inspection lifecycle and provider-neutral
// scheduling contracts. Upstream Auth and HTTP client details stay behind host
// adapters in the management package.
package inspection

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrPaused = errors.New("account inspection is paused")

// Lifecycle is a cooperative gate shared by scheduled runs, one-shot probes
// and manual actions. Backup imports pause the gate and wait until all admitted
// work exits before restoring schedule and snapshot state.
type Lifecycle struct {
	mu     sync.Mutex
	paused bool
	active int
}

func (l *Lifecycle) Begin() (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	l.mu.Lock()
	if l.paused {
		l.mu.Unlock()
		return nil, ErrPaused
	}
	l.active++
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.active > 0 {
				l.active--
			}
			l.mu.Unlock()
		})
	}, nil
}

func (l *Lifecycle) Pause(ctx context.Context) error {
	return l.PauseAndCancel(ctx, nil)
}

func (l *Lifecycle) PauseAndCancel(ctx context.Context, cancel func()) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	startedPause := !l.paused
	l.paused = true
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for {
		l.mu.Lock()
		active := l.active
		l.mu.Unlock()
		if active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			if startedPause {
				_ = l.Resume(context.Background())
			}
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (l *Lifecycle) Resume(context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	l.paused = false
	l.mu.Unlock()
	return nil
}
