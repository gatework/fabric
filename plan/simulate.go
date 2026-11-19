package plan

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// ErrNotConverged means simulating a plan does not reach the desired state.
var ErrNotConverged = errors.New("plan: simulation does not reach the desired state")

// ErrCollateral means a readback changed objects outside the plan.
var ErrCollateral = errors.New("plan: objects outside the plan changed")

// Simulate applies ops in order to a copy of cfg.
func Simulate(cfg *device.Config, ops []Op) (*device.Config, error) {
	out := cfg.Clone()
	for _, op := range ops {
		if err := op.Apply(out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Converges proves ops: applied to cfg they leave nothing to change.
func Converges(d *Desired, cfg *device.Config, ops []Op, opt Options) error {
	sim, err := Simulate(cfg, ops)
	if err != nil {
		return err
	}
	if rest := Diff(d, sim, opt); len(rest) > 0 {
		refs := make([]string, 0, len(rest))
		for _, op := range rest {
			refs = append(refs, op.Ref().String())
		}
		return fmt.Errorf("%w: %s", ErrNotConverged, strings.Join(refs, ", "))
	}
	return Untouched(cfg, sim, ops)
}

// Untouched reports objects other than the operations' targets that
// differ between before and after.
func Untouched(before, after *device.Config, ops []Op) error {
	a, b := mask(before, ops), mask(after, ops)
	if reflect.DeepEqual(a, b) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrCollateral, firstDifference(a, b))
}

// mask removes every object an operation targets, so that two
// configurations compare equal when only those objects differ.
func mask(cfg *device.Config, ops []Op) *device.Config {
	m := cfg.Clone()
	m.Fingerprint = ""
	// "vlan 1,10-20" summary lines change with any VLAN; VLAN objects
	// themselves are compared individually.
	for _, op := range ops {
		ref := op.Ref()
		switch ref.Kind {
		case KindFeature:
			delete(m.Features, ref.Key)
		case KindSetting:
			m.Set(device.Setting(ref.Key), "")
		case KindNTPServer:
			_ = NTPServerOp{Address: ref.Key}.Apply(m)
		case KindLogServer:
			_ = LogServerOp{Address: ref.Key}.Apply(m)
		case KindVRF:
			delete(m.VRFs, ref.Key)
		case KindVLAN:
			if v, err := fabric.ParseVLAN(ref.Key); err == nil {
				delete(m.VLANs, v)
			}
		case KindEVPN:
			if v, err := fabric.ParseVNI(ref.Key); err == nil {
				delete(m.EVPN, v)
			}
		case KindNVEMember:
			if v, err := fabric.ParseVNI(ref.Key); err == nil && m.NVE != nil {
				delete(m.NVE.Members, v)
			}
		case KindSVI:
			delete(m.Interfaces, "Vlan"+ref.Key)
		case KindBGPVRF:
			if m.BGP != nil {
				delete(m.BGP.VRFs, ref.Key)
			}
		case KindPort, KindPortChannel, KindInterface, KindInterfaceRst:
			delete(m.Interfaces, ref.Key)
		case KindOSPF:
			delete(m.OSPF, ref.Key)
		case KindBGP, KindNVE, KindBGPTemplate, KindBGPNeighbor, KindVPCDomain:
			maskUnderlay(m, op)
		}
	}
	return m
}

// firstDifference names the first top-level field that differs, for a
// diagnostic that carries no configuration values.
func firstDifference(a, b *device.Config) string {
	va, vb := reflect.ValueOf(a).Elem(), reflect.ValueOf(b).Elem()
	for i := range va.NumField() {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			return va.Type().Field(i).Name
		}
	}
	return "unknown"
}

// maskUnderlay removes singleton objects an operation creates, or the
// fields it manages on them.
func maskUnderlay(m *device.Config, op Op) {
	switch o := op.(type) {
	case BGPOp:
		if o.Have == nil {
			m.BGP = nil
		} else if m.BGP != nil {
			b := *m.BGP
			b.RouterID = ""
			m.BGP = &b
		}
	case BGPPeerOp:
		if m.BGP != nil {
			if o.Template {
				delete(m.BGP.Templates, o.Key)
			} else {
				delete(m.BGP.Neighbors, o.Key)
			}
		}
	case NVEOp:
		if o.Have == nil {
			m.NVE = nil
		} else if m.NVE != nil {
			n := *m.NVE
			n.Name, n.Shutdown, n.SourceInterface, n.HostReachabilityBGP = "", false, "", false
			m.NVE = &n
		}
	case VPCDomainOp:
		if o.Have == nil {
			m.VPC = nil
		} else if m.VPC != nil {
			d := *m.VPC
			d.ID, d.KeepaliveDest, d.KeepaliveSource = 0, "", ""
			d.VirtualPeerLink, d.VirtualPeerLinkSource, d.VirtualPeerLinkDSCP = "", "", 0
			m.VPC = &d
		}
	}
}
