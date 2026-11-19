package status

import (
	"cmp"
	"math"
	"net/netip"
	"slices"

	"github.com/gatework/go-netops/assess"
	"github.com/gatework/go-netops/evidence"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// Level is a health verdict.
type Level uint8

// Levels, worst last except Unknown, which means "not evidenced".
const (
	LevelUnknown Level = iota
	LevelHealthy
	LevelDegraded
	LevelFailed
)

// String returns the Chinese label.
func (l Level) String() string {
	switch l {
	case LevelHealthy:
		return "正常"
	case LevelDegraded:
		return "降级"
	case LevelFailed:
		return "故障"
	case LevelUnknown:
		return "未知"
	default:
		return "未知"
	}
}

// MarshalText encodes the label.
func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// Ratio is how many of the expected items are up. Known is false when the
// evidence was incomplete; Total zero means nothing is expected.
type Ratio struct {
	Up    int  `json:"up"`
	Total int  `json:"total"`
	Known bool `json:"known"`
}

func (r Ratio) value() (float64, bool) {
	if !r.Known || r.Total == 0 {
		return 0, false
	}
	return float64(min(r.Up, r.Total)) / float64(r.Total), true
}

// Switch is one switch's health.
type Switch struct {
	Name     string               `json:"name"`
	Region   string               `json:"region"`
	Role     fabric.Role          `json:"role"`
	NVEPeers Ratio                `json:"nve_peers"`
	Sessions Ratio                `json:"evpn_sessions"`
	OSPF     Ratio                `json:"ospf"`
	VNIs     Ratio                `json:"vnis"`
	VPC      Ratio                `json:"vpc"`
	Bundles  Ratio                `json:"bundles"`
	Findings map[string]int       `json:"findings"`
	Drift    int                  `json:"intent_drift"`
	Baseline int                  `json:"baseline_drift"`
	Score    int                  `json:"score"`
	Level    Level                `json:"level"`
	Missing  []evidence.Dimension `json:"missing,omitempty"`
}

// Region aggregates its switches.
type Region struct {
	Name     string         `json:"name"`
	Switches []Switch       `json:"switches"`
	Levels   map[string]int `json:"levels"`
	// Worst and Mean are the lowest and the average known switch scores.
	Worst int   `json:"worst"`
	Mean  int   `json:"mean"`
	Level Level `json:"level"`
}

// Report is every region's health.
type Report struct {
	Regions []Region `json:"regions"`
}

// Input is what one region's health is computed from.
type Input struct {
	Name     string
	Intent   *fabric.Region
	Switches []*device.Switch
	// Findings are the assessment findings; each is attributed to the
	// switch named in its subject.
	Findings []assess.Finding
}

// Build computes the report.
func Build(regions []Input) Report {
	var rep Report
	for _, in := range regions {
		rep.Regions = append(rep.Regions, region(in))
	}
	return rep
}

func region(in Input) Region {
	r := Region{Name: in.Name, Levels: map[string]int{}}
	vteps := vtepAddresses(in.Switches)
	spines := routerIDs(in.Switches, func(r fabric.Role) bool { return r == fabric.RoleSpine })
	vtepIDs := routerIDs(in.Switches, fabric.Role.VTEP)
	byName := findingsBySwitch(in.Findings)
	total, known := 0, 0
	r.Worst = 100
	for _, sw := range in.Switches {
		s := health(in, sw, peers{vteps: vteps, spines: spines, vtepIDs: vtepIDs}, byName[sw.Name])
		r.Switches = append(r.Switches, s)
		r.Levels[s.Level.String()]++
		if s.Level != LevelUnknown {
			total += s.Score
			known++
			r.Worst = min(r.Worst, s.Score)
		}
	}
	slices.SortFunc(r.Switches, func(a, b Switch) int { return cmp.Or(cmp.Compare(a.Score, b.Score), cmp.Compare(a.Name, b.Name)) })
	if known == 0 {
		r.Worst = 0
		return r
	}
	r.Mean = total / known
	r.Level = levelOf(r.Worst, false)
	return r
}

func findingsBySwitch(fs []assess.Finding) map[string][]assess.Finding {
	out := map[string][]assess.Finding{}
	for _, f := range fs {
		for s := &f.Subject; s != nil; s = s.Parent {
			if s.Kind == "switch" {
				out[s.Key] = append(out[s.Key], f)
				break
			}
		}
	}
	return out
}

func vtepAddresses(switches []*device.Switch) map[string]netip.Addr {
	out := map[string]netip.Addr{}
	for _, sw := range switches {
		if sw.Role.VTEP() && sw.Config != nil {
			if a, ok := sw.Config.VTEPAddress(); ok {
				out[sw.Name] = a
			}
		}
	}
	return out
}

func routerIDs(switches []*device.Switch, role func(fabric.Role) bool) map[string]bool {
	out := map[string]bool{}
	for _, sw := range switches {
		if role(sw.Role) && sw.Config != nil && sw.Config.BGP != nil {
			out[sw.Config.BGP.RouterID] = true
		}
	}
	return out
}

// peers is what a switch is expected to peer with.
type peers struct {
	vteps   map[string]netip.Addr
	spines  map[string]bool
	vtepIDs map[string]bool
}

func health(in Input, sw *device.Switch, p peers, findings []assess.Finding) Switch {
	s := Switch{Name: sw.Name, Region: in.Name, Role: sw.Role, Findings: map[string]int{}}
	for _, d := range device.Dimensions() {
		if st := sw.Coverage.Status(d); !st.Complete() && st != evidence.StatusNotRequested {
			s.Missing = append(s.Missing, d)
		}
	}
	has := sw.Has
	if sw.Role.VTEP() {
		s.NVEPeers = nvePeers(sw, p.vteps, has(device.DimNVEPeers))
		s.Sessions = sessionsTo(sw, p.spines, len(p.spines), has(device.DimBGP))
		s.VNIs = vnis(sw, has(device.DimNVEVNIs))
	} else {
		// Spines carry no overlay: nothing is expected there.
		s.NVEPeers, s.VNIs = Ratio{Known: true}, Ratio{Known: true}
		s.Sessions = sessionsTo(sw, p.vtepIDs, len(p.vtepIDs), has(device.DimBGP))
	}
	s.OSPF = ospf(in.Intent, sw, has(device.DimOSPF))
	s.VPC = vpc(sw, has(device.DimVPC))
	s.Bundles = bundles(sw, has(device.DimPortChannels))
	for _, f := range findings {
		s.Findings[f.Severity.String()]++
		switch f.Rule {
		case "fabric.intent_drift":
			s.Drift++
		case "fabric.baseline_drift":
			s.Baseline++
		}
	}
	s.Score, s.Level = score(s)
	return s
}

func nvePeers(sw *device.Switch, vteps map[string]netip.Addr, known bool) Ratio {
	own := vteps[sw.Name]
	want := map[netip.Addr]bool{}
	for _, a := range vteps {
		if a != own {
			want[a] = true
		}
	}
	up := 0
	for _, p := range sw.State.NVEPeers {
		if a, err := netip.ParseAddr(p.Peer); err == nil && p.Up && want[a] {
			up++
		}
	}
	return Ratio{Up: up, Total: len(want), Known: known}
}

func sessionsTo(sw *device.Switch, peers map[string]bool, want int, known bool) Ratio {
	up := 0
	for _, n := range sw.State.BGP.Neighbors {
		if n.Established && peers[n.Address] {
			up++
		}
	}
	return Ratio{Up: up, Total: want, Known: known}
}

func vnis(sw *device.Switch, known bool) Ratio {
	r := Ratio{Known: known && sw.Config != nil}
	if sw.Config == nil || sw.Config.NVE == nil {
		return r
	}
	r.Total = len(sw.Config.NVE.Members)
	up := map[fabric.VNI]bool{}
	for _, v := range sw.State.NVEVNIs {
		if v.Up {
			up[v.VNI] = true
		}
	}
	for vni := range sw.Config.NVE.Members {
		if up[vni] {
			r.Up++
		}
	}
	return r
}

// ospf counts full underlay adjacencies against the declared links, or
// against all adjacencies when the underlay is not managed.
func ospf(intent *fabric.Region, sw *device.Switch, known bool) Ratio {
	process := ""
	want := len(sw.State.OSPF)
	if intent != nil && intent.Underlay != nil {
		process, want = intent.Underlay.OSPFProcess, len(intent.LinksOf(sw.Name))
	}
	full := 0
	for _, n := range sw.State.OSPF {
		if n.Full && (process == "" || n.Process == process) {
			full++
		}
	}
	return Ratio{Up: full, Total: want, Known: known}
}

func vpc(sw *device.Switch, known bool) Ratio {
	v := sw.State.VPC
	if v == nil {
		return Ratio{Known: known}
	}
	ok := 0
	if v.PeerOK && v.KeepaliveOK && v.ConsistencyOK {
		ok = 1
	}
	return Ratio{Up: ok, Total: 1, Known: known}
}

func bundles(sw *device.Switch, known bool) Ratio {
	r := Ratio{Known: known}
	for _, pc := range sw.State.PortChannels {
		if !pc.Up {
			continue
		}
		for _, m := range pc.Members {
			r.Total++
			if m.Bundled {
				r.Up++
			}
		}
	}
	return r
}

// Weights of findings against the 40 compliance points.
func penalty(sev string) float64 {
	switch sev {
	case "critical":
		return 20
	case "high":
		return 8
	case "medium":
		return 3
	case "low":
		return 1
	default:
		return 0
	}
}

// score is 60 points of control plane (the mean of known indicators) and
// 40 of compliance (minus weighted findings); see ADR 0009.
func score(s Switch) (int, Level) {
	var sum float64
	n := 0
	for _, r := range []Ratio{s.NVEPeers, s.Sessions, s.OSPF, s.VNIs, s.VPC, s.Bundles} {
		if v, ok := r.value(); ok {
			sum += v
			n++
		}
	}
	if n == 0 && len(s.Missing) > 0 {
		return 0, LevelUnknown
	}
	control := 60.0
	if n > 0 {
		control = 60 * sum / float64(n)
	}
	compliance := 40.0
	for sev, count := range s.Findings {
		compliance -= penalty(sev) * float64(count)
	}
	total := int(math.Round(control + max(compliance, 0)))
	return total, levelOf(total, s.Findings["critical"] > 0)
}

func levelOf(score int, critical bool) Level {
	switch {
	case critical || score < 60:
		return LevelFailed
	case score < 90:
		return LevelDegraded
	default:
		return LevelHealthy
	}
}
