package parallel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gatework/fabric/internal/parallel"
)

func TestMapKeepsOrder(t *testing.T) {
	t.Parallel()
	in := make([]int, 500)
	for i := range in {
		in[i] = i
	}
	out, errs := parallel.Map(t.Context(), 7, in, func(_ context.Context, v int) (int, error) { return v * v, nil })
	for i, v := range out {
		if v != i*i || errs[i] != nil {
			t.Fatalf("out[%d] = %d %v", i, v, errs[i])
		}
	}
}

func TestMapStopsOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, errs := parallel.Map(ctx, 2, []int{1, 2, 3}, func(context.Context, int) (int, error) { return 0, nil })
	for _, err := range errs {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	}
}
