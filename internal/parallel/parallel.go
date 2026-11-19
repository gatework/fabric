// Package parallel runs independent work on a bounded pool of goroutines
// and returns results in input order, so concurrent output is identical
// to sequential output.
package parallel

import (
	"context"
	"runtime"
	"sync"
)

// Map calls fn for every item on at most workers goroutines (GOMAXPROCS
// when workers <= 0) and returns the results and errors by index. It
// stops starting new items once ctx is done; their errors are ctx.Err().
func Map[T, R any](ctx context.Context, workers int, items []T, fn func(context.Context, T) (R, error)) ([]R, []error) {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, len(items))
	out := make([]R, len(items))
	errs := make([]error, len(items))
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range next {
				out[i], errs[i] = fn(ctx, items[i])
			}
		})
	}
	for i := range items {
		if err := ctx.Err(); err != nil {
			errs[i] = err
			continue
		}
		next <- i
	}
	close(next)
	wg.Wait()
	return out, errs
}
