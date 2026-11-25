package fanbox

import (
	"context"
	"sync"
)

// runDownloadTasks stops dispatching after the first failure and joins active work.
func runDownloadTasks(ctx context.Context, tasks []func() error, workers int) error {
	if workers <= 1 {
		for _, task := range tasks {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := task(); err != nil {
				return err
			}
		}
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
dispatch:
	for _, task := range tasks {
		select {
		case <-ctx.Done():
			break dispatch
		case sem <- struct{}{}:
		}
		if ctx.Err() != nil {
			<-sem
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := task(); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
