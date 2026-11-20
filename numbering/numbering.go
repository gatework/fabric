package numbering

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/gatework/netops/set"
)

// ErrExhausted reports a pool without a free value.
var ErrExhausted = errors.New("numbering: pool exhausted")

// maxScan bounds address scans; a /16 pool is the largest expected.
const maxScan = 1 << 20

// Lowest returns the lowest value of pool that is not in used.
func Lowest[T set.Integer](pool, used *set.Set[T]) (T, error) {
	free := pool
	if used != nil {
		free = pool.Difference(used)
	}
	v, ok := free.Min()
	if !ok {
		return v, fmt.Errorf("%w: %s", ErrExhausted, pool.Compact())
	}
	return v, nil
}

// Of builds a set from single values.
func Of[T set.Integer](values ...T) *set.Set[T] {
	s := set.New[T]()
	for _, v := range values {
		s.Add(v, v)
	}
	return s
}

// Addresses allocates from one prefix.
type Addresses struct {
	prefix netip.Prefix
	used   map[netip.Addr]bool
}

// NewAddresses returns a pool over prefix with the given addresses taken.
func NewAddresses(prefix netip.Prefix, used []netip.Addr) (*Addresses, error) {
	if !prefix.IsValid() {
		return nil, fmt.Errorf("numbering: no pool prefix")
	}
	p := &Addresses{prefix: prefix.Masked(), used: map[netip.Addr]bool{}}
	for _, a := range used {
		p.used[a] = true
	}
	return p, nil
}

// Host returns the lowest free address of the pool, skipping the network
// address, and marks it used.
func (p *Addresses) Host() (netip.Addr, error) {
	a := p.prefix.Addr().Next()
	for range maxScan {
		if !p.prefix.Contains(a) {
			break
		}
		if !p.used[a] {
			p.used[a] = true
			return a, nil
		}
		a = a.Next()
	}
	return netip.Addr{}, fmt.Errorf("%w: %s", ErrExhausted, p.prefix)
}

// Subnet returns the lowest aligned subnet of length bits with no used
// address, and marks its addresses used. The endpoints of a link subnet
// are LinkEnds(subnet).
func (p *Addresses) Subnet(bits int) (netip.Prefix, error) {
	if bits < p.prefix.Bits() || bits > p.prefix.Addr().BitLen() {
		return netip.Prefix{}, fmt.Errorf("numbering: /%d does not fit %s", bits, p.prefix)
	}
	size := 1 << (p.prefix.Addr().BitLen() - bits)
	start := p.prefix.Addr()
	for range maxScan / size {
		sub := netip.PrefixFrom(start, bits)
		if !p.prefix.Contains(start) {
			break
		}
		if p.free(sub, size) {
			p.take(sub, size)
			return sub, nil
		}
		next, ok := advance(start, size)
		if !ok {
			break
		}
		start = next
	}
	return netip.Prefix{}, fmt.Errorf("%w: no free /%d in %s", ErrExhausted, bits, p.prefix)
}

func (p *Addresses) free(sub netip.Prefix, size int) bool {
	a := sub.Addr()
	for range size {
		if p.used[a] {
			return false
		}
		a = a.Next()
	}
	return true
}

func (p *Addresses) take(sub netip.Prefix, size int) {
	a := sub.Addr()
	for range size {
		p.used[a] = true
		a = a.Next()
	}
}

func advance(a netip.Addr, n int) (netip.Addr, bool) {
	for range n {
		a = a.Next()
		if !a.IsValid() {
			return a, false
		}
	}
	return a, true
}

// LinkEnds returns the two usable addresses of a /31 or /30 link subnet.
func LinkEnds(sub netip.Prefix) (netip.Prefix, netip.Prefix) {
	first := sub.Addr()
	if sub.Bits() == 30 {
		first = first.Next()
	}
	return netip.PrefixFrom(first, sub.Bits()), netip.PrefixFrom(first.Next(), sub.Bits())
}
