package plan

import (
	"reflect"

	"github.com/gatework/fabric/device"
)

// diffUnderlay compares the routed underlay and overlay control plane.
func diffUnderlay(d *Desired, cfg *device.Config) []Op {
	var ops []Op
	for _, p := range sortedKeys(d.OSPF) {
		have, ok := cfg.OSPF[p]
		want := have
		if r := d.OSPF[p].RouterID; r != "" {
			want.RouterID = r
		}
		switch {
		case !ok:
			want = d.OSPF[p]
			ops = append(ops, OSPFOp{Process: p, Want: &want})
		case !reflect.DeepEqual(have, want):
			ops = append(ops, OSPFOp{Process: p, Have: &have, Want: &want})
		}
	}
	ops = append(ops, diffBGPProcess(d, cfg)...)
	for _, name := range sortedKeys(d.Interfaces) {
		i, ok := cfg.Interfaces[name]
		if !ok {
			want := mergeL3(L3{}, d.Interfaces[name])
			ops = append(ops, InterfaceOp{Interface: name, Want: &want})
			continue
		}
		have := l3Of(i)
		if want := mergeL3(have, d.Interfaces[name]); want != have {
			ops = append(ops, InterfaceOp{Interface: name, Have: &have, Want: &want})
		}
	}
	if d.NVE != nil {
		have, ok := nveOf(cfg)
		want := mergeNVE(have, *d.NVE)
		switch {
		case !ok:
			ops = append(ops, NVEOp{Want: &want})
		case want != have:
			ops = append(ops, NVEOp{Have: &have, Want: &want})
		}
	}
	ops = append(ops, diffPeers(d, cfg, true)...)
	ops = append(ops, diffPeers(d, cfg, false)...)
	if d.VPC != nil {
		have, ok := vpcOf(cfg)
		want := mergeVPC(have, *d.VPC)
		switch {
		case !ok:
			ops = append(ops, VPCDomainOp{Want: &want})
		case want != have:
			ops = append(ops, VPCDomainOp{Have: &have, Want: &want})
		}
	}
	return ops
}

func diffBGPProcess(d *Desired, cfg *device.Config) []Op {
	if d.BGP == nil {
		return nil
	}
	have, ok := bgpOf(cfg)
	want := *d.BGP
	switch {
	case !ok:
		return []Op{BGPOp{Want: &want}}
	case want != have:
		return []Op{BGPOp{Have: &have, Want: &want}}
	default:
		return nil
	}
}

func diffPeers(d *Desired, cfg *device.Config, templates bool) []Op {
	desired := d.BGPNeighbors
	if templates {
		desired = d.BGPTemplates
	}
	if len(desired) == 0 {
		return nil
	}
	asn := d.BGPASN
	var observed map[string]device.BGPPeer
	if cfg.BGP != nil {
		asn = cfg.BGP.ASN
		observed = cfg.BGP.Neighbors
		if templates {
			observed = cfg.BGP.Templates
		}
	}
	if d.BGP != nil {
		asn = d.BGP.ASN
	}
	var ops []Op
	for _, key := range sortedKeys(desired) {
		have, ok := observed[key]
		want := mergePeer(have, desired[key])
		switch {
		case !ok:
			want = mergePeer(device.BGPPeer{}, desired[key])
			ops = append(ops, BGPPeerOp{Template: templates, Key: key, ASN: asn, Want: &want})
		case !reflect.DeepEqual(have, want):
			ops = append(ops, BGPPeerOp{Template: templates, Key: key, ASN: asn, Have: &have, Want: &want})
		}
	}
	return ops
}
