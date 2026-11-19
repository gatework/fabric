package workflow

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/gatework/go-netops/session"
	"github.com/gatework/go-netops/store"

	"github.com/gatework/fabric/adapter/snapshot"
	"github.com/gatework/fabric/vendors/nxos"
)

// Collector reads every dimension of one switch.
type Collector interface {
	Collect(ctx context.Context) (map[string]string, error)
}

// CollectRequest collects switches into a snapshot directory.
type CollectRequest struct {
	Targets  []netip.Addr
	Dial     func(netip.Addr) Collector
	Store    *store.Store
	Parallel int
	Now      func() time.Time
	Logger   *slog.Logger
}

// CollectResult is one switch's collection outcome.
type CollectResult struct {
	Mgmt     string `json:"mgmt"`
	Commands int    `json:"commands"`
	Error    string `json:"error,omitempty"`
}

// Collect reads every target concurrently and publishes each switch's
// receipts in the snapshot layout. A failed switch keeps the outputs read
// before the failure; its missing dimensions stay not requested.
func Collect(ctx context.Context, req CollectRequest) []CollectResult {
	if req.Parallel <= 0 {
		req.Parallel = 8
	}
	if req.Now == nil {
		req.Now = time.Now
	}
	if req.Logger == nil {
		req.Logger = slog.New(slog.DiscardHandler)
	}
	out := make([]CollectResult, len(req.Targets))
	sem := make(chan struct{}, req.Parallel)
	var wg sync.WaitGroup
	for i, addr := range req.Targets {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[i] = CollectResult{Mgmt: addr.String(), Error: session.Code(ctx.Err())}
				return
			}
			defer func() { <-sem }()
			out[i] = collectOne(ctx, req, addr)
		})
	}
	wg.Wait()
	return out
}

func collectOne(ctx context.Context, req CollectRequest, addr netip.Addr) CollectResult {
	res := CollectResult{Mgmt: addr.String()}
	outs, err := req.Dial(addr).Collect(ctx)
	if err != nil {
		res.Error = session.Code(err)
	}
	commands := make([]string, 0, len(nxos.Probes()))
	for _, p := range nxos.Probes() {
		commands = append(commands, p.Command)
	}
	files, encErr := snapshot.Encode(addr.String(), commands, outs, "", req.Now())
	if encErr != nil {
		res.Error = encErr.Error()
		return res
	}
	for rel, body := range files {
		if err := req.Store.Publish(ctx, rel, body); err != nil {
			res.Error = session.Code(err)
			return res
		}
	}
	res.Commands = len(files) / 2
	req.Logger.InfoContext(ctx, "switch collected", "mgmt", res.Mgmt, "commands", res.Commands, "error", res.Error)
	return res
}
