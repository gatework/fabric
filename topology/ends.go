package topology

import (
	"fmt"
	"strings"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/device"
)

// End is one end of a point-to-point fabric link as the switch reports
// and configures it.
type End struct {
	Switch    string `json:"switch"`
	Interface string `json:"interface"`
	// Layer is "L3" for a routed port, "L2" for a switchport, empty when
	// the configuration was not collected.
	Layer string `json:"layer,omitempty"`
	// Status, Reason and Speed come from "show interface brief".
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
	Speed  string `json:"speed,omitempty"`
	Up     bool   `json:"up"`
	// Address is the configured address, Intended the intent's.
	Address  string `json:"address,omitempty"`
	Intended string `json:"intended,omitempty"`
	MTU      int    `json:"mtu,omitempty"`
	// Shutdown is set when the configuration shuts the port down.
	Shutdown bool `json:"shutdown,omitempty"`
}

func (e *evidenceSet) end(le fabric.LinkEnd) End {
	out := End{Switch: le.Switch, Interface: le.Interface}
	if le.Address.IsValid() {
		out.Intended = le.Address.String()
	}
	if st, ok := e.ports[portKey{le.Switch, le.Interface}]; ok {
		out.Status, out.Speed, out.Up = st.Status, st.Speed, st.Up
		if st.Reason != "none" {
			out.Reason = st.Reason
		}
	}
	sw, ok := e.byName[le.Switch]
	if !ok || !sw.Configured() {
		return out
	}
	i, ok := sw.Config.Interfaces[le.Interface]
	if !ok {
		return out
	}
	out.Layer, out.MTU = "L3", i.MTU
	if i.Layer2 {
		out.Layer = "L2"
	}
	out.Shutdown = i.Admin == device.AdminDown
	if p, ok := i.Primary(); ok {
		out.Address = p.String()
	}
	return out
}

// endIssues are the configuration faults of one end that need no peer to
// detect: a fabric link is a routed point-to-point port carrying the
// intent's address.
func endIssues(x End, configured bool) []string {
	if !configured {
		return nil
	}
	var out []string
	name := x.Switch + " " + x.Interface
	switch x.Layer {
	case "":
		out = append(out, name+" 未配置")
	case "L2":
		out = append(out, name+" 配置为二层（switchport），fabric 互联须为三层路由口")
	}
	if x.Shutdown {
		out = append(out, name+" 配置了 shutdown")
	}
	switch {
	case x.Layer == "L3" && x.Address == "":
		out = append(out, name+" 未配置互联地址")
	case x.Intended != "" && x.Address != "" && x.Address != x.Intended:
		out = append(out, fmt.Sprintf("%s 地址 %s，意图为 %s", name, x.Address, x.Intended))
	}
	return out
}

// Describe renders an end for operators: "Ethernet1/50 L3 10.0.0.1/31 up 100G".
func (x End) Describe() string {
	parts := []string{x.Interface}
	if x.Layer != "" {
		parts = append(parts, x.Layer)
	}
	switch {
	case x.Address != "":
		parts = append(parts, x.Address)
	case x.Intended != "":
		parts = append(parts, "意图 "+x.Intended)
	}
	if x.Status != "" {
		s := x.Status
		if x.Reason != "" && !x.Up {
			s += "（" + x.Reason + "）"
		}
		parts = append(parts, s)
	}
	if x.Speed != "" && x.Up {
		parts = append(parts, x.Speed)
	}
	if x.MTU > 0 {
		parts = append(parts, fmt.Sprintf("MTU %d", x.MTU))
	}
	return strings.Join(parts, " ")
}
