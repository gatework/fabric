package numbering_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/gatework/set"

	"github.com/gatework/fabric/numbering"
)

func TestLowest(t *testing.T) {
	t.Parallel()
	pool := set.New(set.Range[uint16]{Min: 1, Max: 5})
	if v, err := numbering.Lowest(pool, numbering.Of[uint16](1, 2, 4)); err != nil || v != 3 {
		t.Fatalf("lowest = %d %v", v, err)
	}
	if _, err := numbering.Lowest(pool, pool); !errors.Is(err, numbering.ErrExhausted) {
		t.Fatalf("exhausted: %v", err)
	}
}

func TestHostsAndSubnets(t *testing.T) {
	t.Parallel()
	p, err := numbering.NewAddresses(netip.MustParsePrefix("10.0.0.0/29"), []netip.Addr{netip.MustParseAddr("10.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := p.Host(); a.String() != "10.0.0.2" {
		t.Errorf("host = %s", a)
	}
	links, _ := numbering.NewAddresses(netip.MustParsePrefix("10.1.0.0/28"), []netip.Addr{netip.MustParseAddr("10.1.0.1"), netip.MustParseAddr("10.1.0.6")})
	s1, _ := links.Subnet(30)
	s2, _ := links.Subnet(30)
	if s1.String() != "10.1.0.8/30" || s2.String() != "10.1.0.12/30" {
		t.Errorf("subnets = %s %s", s1, s2)
	}
	if _, err := links.Subnet(30); !errors.Is(err, numbering.ErrExhausted) {
		t.Errorf("exhausted: %v", err)
	}
	a, b := numbering.LinkEnds(netip.MustParsePrefix("10.1.0.8/30"))
	c, d := numbering.LinkEnds(netip.MustParsePrefix("10.1.0.8/31"))
	if a.String() != "10.1.0.9/30" || b.String() != "10.1.0.10/30" || c.String() != "10.1.0.8/31" || d.String() != "10.1.0.9/31" {
		t.Errorf("ends = %s %s %s %s", a, b, c, d)
	}
}

func FuzzSubnet(f *testing.F) {
	f.Add(uint8(30), uint16(3))
	f.Fuzz(func(t *testing.T, bits uint8, taken uint16) {
		var used []netip.Addr
		a := netip.MustParseAddr("10.0.0.0")
		for i := range int(taken % 64) {
			if i%3 == 0 {
				used = append(used, a)
			}
			a = a.Next()
		}
		p, _ := numbering.NewAddresses(netip.MustParsePrefix("10.0.0.0/26"), used)
		sub, err := p.Subnet(int(bits%8) + 26)
		if err != nil {
			return
		}
		for _, u := range used {
			if sub.Contains(u) {
				t.Fatalf("%s overlaps used %s", sub, u)
			}
		}
	})
}
