package provision

import (
	"fmt"
	"net/netip"
	"slices"

	"github.com/gatework/set"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/numbering"
)

// Default pools when a region declares none.
func defaultVLANs() *set.Set[uint16] { return set.New(set.Range[uint16]{Min: 2, Max: 3967}) }

func defaultL3VNIs() *set.Set[uint32] { return set.New(set.Range[uint32]{Min: 50000, Max: 59999}) }

// AddNetwork adds a distributed anycast gateway: a VLAN mapped to an L2VNI
// on every VTEP of the region (or the listed ones), routed in a tenant.
type AddNetwork struct {
	Region string
	// VLAN and VNI are allocated when zero.
	VLAN    fabric.VLAN
	VNI     fabric.VNI
	Name    string
	Tenant  string
	Gateway netip.Prefix
	ACLIn   string
	// Switches limits the network; empty means every VTEP.
	Switches []string
}

// Apply validates and adds the network.
func (q AddNetwork) Apply(in *fabric.Intent, obs *Observation) (Result, error) {
	r, err := region(in, q.Region)
	if err != nil {
		return Result{}, err
	}
	res := Result{Region: r.Name}
	if q.Gateway.IsValid() && q.Tenant == "" {
		return res, fmt.Errorf("%w: a gateway needs a tenant", ErrConflict)
	}
	if q.Tenant != "" {
		if _, ok := r.Tenant(q.Tenant); !ok {
			return res, fmt.Errorf("%w: tenant %s", ErrNotFound, q.Tenant)
		}
	}
	vlan, err := allocateVLAN(r, obs, q.VLAN, &res)
	if err != nil {
		return res, err
	}
	n := &fabric.Network{Name: q.Name, VLAN: vlan, VNI: q.VNI, Tenant: q.Tenant, ACLIn: q.ACLIn, Switches: slices.Clone(q.Switches)}
	if q.Gateway.IsValid() {
		if err := gatewayFree(r, obs, q.Gateway); err != nil {
			return res, err
		}
		n.Gateway = fabric.PrefixOf(q.Gateway)
	}
	vni, ok := r.VNIOf(n)
	if !ok {
		return res, fmt.Errorf("%w: no VNI given and the region has no numbering.l2vni_base", ErrConflict)
	}
	if q.VNI == 0 {
		res.allocate("vni", vni)
	}
	r.Networks = append(r.Networks, n)
	res.change("networks["+vlan.String()+"]", "新增网络 %s：VLAN %s → L2VNI %s%s", orDash(q.Name), vlan, vni, gatewayText(n))
	for _, s := range r.VTEPs() {
		if r.Carries(n, s) {
			res.affect(s.Name)
		}
	}
	return res, nil
}

func gatewayText(n *fabric.Network) string {
	if !n.Gateway.IsValid() {
		return "（二层）"
	}
	return fmt.Sprintf("，分布式网关 %s（租户 %s）", n.Gateway, n.Tenant)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func usedVLANs(r *fabric.Region, obs *Observation) *set.Set[uint16] {
	used := set.New[uint16]()
	for _, n := range r.Networks {
		used.Add(uint16(n.VLAN), uint16(n.VLAN))
	}
	for _, t := range r.Tenants {
		used.Add(uint16(t.VLAN), uint16(t.VLAN))
	}
	for _, v := range obs.vlans(r) {
		used.Add(uint16(v), uint16(v))
	}
	used.Add(1, 1)
	return used
}

func allocateVLAN(r *fabric.Region, obs *Observation, want fabric.VLAN, res *Result) (fabric.VLAN, error) {
	used := usedVLANs(r, obs)
	if want != 0 {
		if used.Contains(uint16(want)) {
			return 0, fmt.Errorf("%w: vlan %s is already in use", ErrConflict, want)
		}
		return want, nil
	}
	pool := defaultVLANs()
	if r.Underlay != nil && r.Underlay.Pools.VLANs != nil {
		pool = r.Underlay.Pools.VLANs
	}
	v, err := numbering.Lowest(pool, used)
	if err != nil {
		return 0, err
	}
	res.allocate("vlan", v)
	return fabric.VLAN(v), nil
}

// gatewayFree rejects a gateway that overlaps a declared gateway, a fabric
// link or any address configured on the region's switches.
func gatewayFree(r *fabric.Region, obs *Observation, gw netip.Prefix) error {
	for _, n := range r.Networks {
		if n.Gateway.IsValid() && n.Gateway.Overlaps(gw) {
			return fmt.Errorf("%w: gateway %s overlaps network %s (%s)", ErrConflict, gw, n.VLAN, n.Gateway)
		}
	}
	for _, p := range obs.addresses(r) {
		if p.Bits() < 32 && p.Masked().Overlaps(gw.Masked()) {
			return fmt.Errorf("%w: gateway %s overlaps configured address %s", ErrConflict, gw, p)
		}
	}
	return nil
}

// RemoveNetwork retires a network. It refuses while ports carry it or,
// unless Force, while endpoints are learned on it.
type RemoveNetwork struct {
	Region string
	VLAN   fabric.VLAN
	Force  bool
}

// Apply validates and removes the network.
func (q RemoveNetwork) Apply(in *fabric.Intent, obs *Observation) (Result, error) {
	r, err := region(in, q.Region)
	if err != nil {
		return Result{}, err
	}
	res := Result{Region: r.Name}
	n, ok := r.Network(q.VLAN)
	if !ok {
		return res, fmt.Errorf("%w: network %s", ErrNotFound, q.VLAN)
	}
	for _, a := range r.Attachments {
		if slices.Contains(r.Spec(a).Networks, q.VLAN) {
			return res, fmt.Errorf("%w: %s still carries vlan %s", ErrInUse, a.Key(), q.VLAN)
		}
	}
	for _, p := range r.PortProfiles {
		if slices.Contains(p.Networks, q.VLAN) {
			return res, fmt.Errorf("%w: profile %s still carries vlan %s", ErrInUse, p.Name, q.VLAN)
		}
	}
	if endpoints := localEndpoints(r, obs, q.VLAN); endpoints > 0 && !q.Force {
		return res, fmt.Errorf("%w: %d endpoints are still learned on vlan %s (use force)", ErrInUse, endpoints, q.VLAN)
	}
	vni, _ := r.VNIOf(n)
	for _, s := range r.VTEPs() {
		if r.Carries(n, s) {
			res.affect(s.Name)
		}
	}
	r.Networks = slices.DeleteFunc(r.Networks, func(x *fabric.Network) bool { return x == n })
	res.RemovedNetworks = map[fabric.VLAN]fabric.VNI{q.VLAN: vni}
	res.change("networks["+q.VLAN.String()+"]", "下线网络 VLAN %s / L2VNI %s", q.VLAN, vni)
	return res, nil
}

// localEndpoints counts MACs learned on local ports of the VLAN.
func localEndpoints(r *fabric.Region, obs *Observation, vlan fabric.VLAN) int {
	n := 0
	for _, s := range r.Switches {
		st, ok := obs.state(s.Name)
		if !ok {
			continue
		}
		for _, m := range st.MACs {
			if m.VLAN == vlan && m.RemoteVTEP == "" && m.Type == "dynamic" {
				n++
			}
		}
	}
	return n
}

// AddTenant adds a routed VRF with its L3VNI on every VTEP of the region.
type AddTenant struct {
	Region string
	Name   string
	// L3VNI and VLAN are allocated when zero.
	L3VNI fabric.VNI
	VLAN  fabric.VLAN
}

// Apply validates and adds the tenant.
func (q AddTenant) Apply(in *fabric.Intent, obs *Observation) (Result, error) {
	r, err := region(in, q.Region)
	if err != nil {
		return Result{}, err
	}
	res := Result{Region: r.Name}
	if _, ok := r.Tenant(q.Name); ok || q.Name == "" {
		return res, fmt.Errorf("%w: tenant name %q is empty or taken", ErrConflict, q.Name)
	}
	vlan, err := allocateVLAN(r, obs, q.VLAN, &res)
	if err != nil {
		return res, err
	}
	vni := q.L3VNI
	if vni == 0 {
		used := set.New[uint32]()
		for _, t := range r.Tenants {
			used.Add(uint32(t.L3VNI), uint32(t.L3VNI))
		}
		for _, n := range r.Networks {
			if v, ok := r.VNIOf(n); ok {
				used.Add(uint32(v), uint32(v))
			}
		}
		pool := defaultL3VNIs()
		if r.Underlay != nil && r.Underlay.Pools.L3VNIs != nil {
			pool = r.Underlay.Pools.L3VNIs
		}
		v, err := numbering.Lowest(pool, used)
		if err != nil {
			return res, err
		}
		vni = fabric.VNI(v)
		res.allocate("l3vni", vni)
	}
	r.Tenants = append(r.Tenants, &fabric.Tenant{Name: q.Name, L3VNI: vni, VLAN: vlan})
	res.change("tenants["+q.Name+"]", "新增租户 %s：L3VNI %s，核心 VLAN %s", q.Name, vni, vlan)
	for _, s := range r.VTEPs() {
		res.affect(s.Name)
	}
	return res, nil
}
