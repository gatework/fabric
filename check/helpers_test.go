package check_test

import "net/netip"

func parseAddr(s string) (netip.Addr, error) { return netip.ParseAddr(s) }
