package fanbox

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloadTasksStopDispatchAndJoinActiveWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var joined atomic.Bool
	var extra atomic.Int32
	errStopped := errors.New("session rejected")
	tasks := []func() error{
		func() error { close(started); <-release; joined.Store(true); return nil },
		func() error { <-started; cancel(); close(release); return errStopped },
	}
	for range 20 {
		tasks = append(tasks, func() error { extra.Add(1); return nil })
	}
	err := runDownloadTasks(ctx, tasks, 2)
	require.ErrorIs(t, err, errStopped)
	require.True(t, joined.Load(), "active work outlived the crawler")
	require.Zero(t, extra.Load(), "work was dispatched after cancellation")
}

func TestDownloadTasksStopAfterFailure(t *testing.T) {
	errStopped := errors.New("session rejected")
	calls := 0
	err := runDownloadTasks(context.Background(), []func() error{
		func() error { return errStopped },
		func() error { calls++; return nil },
	}, 1)
	require.ErrorIs(t, err, errStopped)
	require.Zero(t, calls)
}
