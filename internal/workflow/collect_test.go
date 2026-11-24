package workflow_test

import (
	"context"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gatework/netops/store"

	"github.com/gatework/fabric/device"
	"github.com/gatework/fabric/internal/workflow"
	"github.com/gatework/fabric/vendors/nxos"
)

type fakeCollector map[string]string

func (f fakeCollector) Collect(context.Context) (map[string]string, error) { return f, nil }

func TestCollectKeepsRawCaptures(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	outs := fakeCollector{
		"show running-config": read(t, "../../vendors/nxos/testdata/leaf.cfg"),
		nxos.RawCaptures()[0]: "BGP summary information for VRF VXLAN, address family IPv4 Unicast\n",
	}
	addr := netip.MustParseAddr("198.51.100.11")
	res := workflow.Collect(t.Context(), workflow.CollectRequest{
		Targets: []netip.Addr{addr}, Dial: func(netip.Addr) workflow.Collector { return outs }, Store: st,
		Now: func() time.Time { return time.Unix(0, 0) },
	})
	if len(res) != 1 || res[0].Error != "" || res[0].Commands != 2 {
		t.Fatalf("collect = %+v", res)
	}
	files, _ := filepath.Glob(filepath.Join(root, addr.String(), "*.output"))
	var raw bool
	for _, f := range files {
		if strings.Contains(f, "show-bgp-ipv4-unicast-summary-vrf-all") {
			raw = true
		}
	}
	if !raw {
		t.Fatalf("raw capture not written: %v", files)
	}
	fleet, err := workflow.LoadSnapshot(t.Context(), root, nil, workflow.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fleet.Switches) != 1 || !fleet.Switches[0].Has(device.DimConfig) {
		t.Fatalf("reloaded snapshot = %+v", fleet.Switches)
	}
}
