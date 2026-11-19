package governance

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gatework/fabric"
	"github.com/gatework/fabric/plan"
)

// Gate errors.
var (
	ErrExpired        = errors.New("governance: plan expired")
	ErrOutsideWindow  = errors.New("governance: outside the maintenance window")
	ErrNoTicket       = errors.New("governance: change ticket required")
	ErrUnacknowledged = errors.New("governance: major change not acknowledged")
	ErrNoReason       = errors.New("governance: an emergency needs a reason")
)

// Class is the change class of a plan.
type Class uint8

// Change classes, by increasing risk.
const (
	// ClassStandard is a routine, low-risk change.
	ClassStandard Class = iota
	// ClassNormal changes existing service state.
	ClassNormal
	// ClassMajor touches the underlay or control plane, removes service
	// with endpoints, or has a wide blast radius.
	ClassMajor
)

// String returns the Chinese label.
func (c Class) String() string {
	switch c {
	case ClassNormal:
		return "常规变更"
	case ClassMajor:
		return "重大变更"
	case ClassStandard:
		return "标准变更"
	default:
		return "标准变更"
	}
}

// MarshalText encodes the label.
func (c Class) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

// UnmarshalText decodes the label.
func (c *Class) UnmarshalText(b []byte) error {
	for _, k := range []Class{ClassStandard, ClassNormal, ClassMajor} {
		if k.String() == string(b) {
			*c = k
			return nil
		}
	}
	return fmt.Errorf("governance: unknown class %q", b)
}

// Item is what risk assessment needs of one switch's plan.
type Item struct {
	Switch  string
	Region  string
	Role    fabric.Role
	Summary map[string]int
	Impact  plan.Impact
}

// Policy tunes classification.
type Policy struct {
	// MajorEndpoints makes a change major when it disturbs more endpoints.
	MajorEndpoints int `json:"major_endpoints"`
}

// DefaultPolicy disturbs at most 50 endpoints before a change is major.
func DefaultPolicy() Policy { return Policy{MajorEndpoints: 50} }

// Risk is a plan's change class and why.
type Risk struct {
	Class     Class    `json:"class"`
	Reasons   []string `json:"reasons,omitempty"`
	Switches  int      `json:"switches"`
	Regions   int      `json:"regions"`
	Endpoints int      `json:"endpoints"`
}

// Assess classifies a plan.
func Assess(items []Item, p Policy) Risk {
	r := Risk{Switches: len(items)}
	regions := map[string]bool{}
	raise := func(c Class, format string, args ...any) {
		r.Class = max(r.Class, c)
		reason := fmt.Sprintf(format, args...)
		if !slices.Contains(r.Reasons, reason) {
			r.Reasons = append(r.Reasons, reason)
		}
	}
	for _, it := range items {
		regions[it.Region] = true
		r.Endpoints += it.Impact.Endpoints
		switch {
		case it.Impact.Underlay:
			raise(ClassMajor, "变更 underlay / 控制面（%s）", it.Switch)
		case it.Role == fabric.RoleSpine:
			raise(ClassMajor, "变更 spine（%s）", it.Switch)
		case it.Impact.Deletes > 0 && it.Impact.Endpoints > 0:
			raise(ClassMajor, "删除在用对象（%s，%d 个终端）", it.Switch, it.Impact.Endpoints)
		case it.Impact.Deletes > 0:
			raise(ClassNormal, "删除对象（%s）", it.Switch)
		case len(it.Impact.Disruptive) > 0:
			raise(ClassNormal, "修改在用网络或端口（%s）", it.Switch)
		case touchesSettings(it.Summary):
			raise(ClassNormal, "修改基线设置（%s）", it.Switch)
		}
	}
	r.Regions = len(regions)
	if r.Regions > 1 {
		raise(ClassMajor, "跨 %d 个区域", r.Regions)
	}
	if p.MajorEndpoints > 0 && r.Endpoints > p.MajorEndpoints {
		raise(ClassMajor, "影响终端 %d 个，超过 %d", r.Endpoints, p.MajorEndpoints)
	}
	slices.Sort(r.Reasons)
	return r
}

func touchesSettings(summary map[string]int) bool {
	for k := range summary {
		if strings.HasSuffix(k, " setting") || strings.HasSuffix(k, " ntp_server") || strings.HasSuffix(k, " logging_server") {
			return true
		}
	}
	return false
}

// Window is a daily maintenance window in a time zone, such as
// 22:00-06:00 Asia/Shanghai; it may span midnight.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Zone  string `json:"zone"`
}

// ParseWindow reads "HH:MM-HH:MM"; zone names an IANA zone ("" is UTC).
func ParseWindow(text, zone string) (*Window, error) {
	start, end, ok := strings.Cut(text, "-")
	w := &Window{Start: strings.TrimSpace(start), End: strings.TrimSpace(end), Zone: zone}
	if !ok {
		return nil, fmt.Errorf("governance: window %q is not HH:MM-HH:MM", text)
	}
	if _, err := time.Parse("15:04", w.Start); err != nil {
		return nil, fmt.Errorf("governance: window start %q: %w", w.Start, err)
	}
	if _, err := time.Parse("15:04", w.End); err != nil {
		return nil, fmt.Errorf("governance: window end %q: %w", w.End, err)
	}
	if _, err := w.location(); err != nil {
		return nil, err
	}
	return w, nil
}

// Contains reports whether t falls inside the window.
func (w *Window) Contains(t time.Time) bool {
	loc, err := w.location()
	if err != nil {
		return false
	}
	now := t.In(loc).Format("15:04")
	if w.Start <= w.End {
		return now >= w.Start && now < w.End
	}
	return now >= w.Start || now < w.End
}

// String formats the window.
func (w *Window) String() string { return w.Start + "-" + w.End + " " + w.Zone }

// Record is the change record of a plan.
type Record struct {
	Ticket    string    `json:"ticket,omitempty"`
	Requester string    `json:"requester,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Window    *Window   `json:"window,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GateOptions are the deploy-time declarations.
type GateOptions struct {
	Now           time.Time
	AckRisk       bool
	Emergency     bool
	Reason        string
	RequireTicket bool
}

// Decision is the gate's verdict, recorded with the execution.
type Decision struct {
	Allowed   bool     `json:"allowed"`
	Refusals  []string `json:"refusals,omitempty"`
	Bypassed  []string `json:"bypassed,omitempty"`
	Emergency bool     `json:"emergency,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

// Gate decides whether a plan may be applied now.
func Gate(rec Record, risk Risk, opt GateOptions) (Decision, error) {
	d := Decision{Emergency: opt.Emergency, Reason: opt.Reason}
	var errs []error
	if !rec.ExpiresAt.IsZero() && opt.Now.After(rec.ExpiresAt) {
		errs = append(errs, fmt.Errorf("%w at %s", ErrExpired, rec.ExpiresAt.Format(time.RFC3339)))
	}
	if opt.RequireTicket && rec.Ticket == "" {
		errs = append(errs, ErrNoTicket)
	}
	if rec.Window != nil && !rec.Window.Contains(opt.Now) {
		switch {
		case opt.Emergency && opt.Reason == "":
			errs = append(errs, ErrNoReason)
		case opt.Emergency:
			d.Bypassed = append(d.Bypassed, "maintenance window "+rec.Window.String())
		default:
			errs = append(errs, fmt.Errorf("%w %s", ErrOutsideWindow, rec.Window))
		}
	}
	if risk.Class == ClassMajor && !opt.AckRisk {
		errs = append(errs, ErrUnacknowledged)
	}
	for _, e := range errs {
		d.Refusals = append(d.Refusals, e.Error())
	}
	d.Allowed = len(errs) == 0
	return d, errors.Join(errs...)
}

func (w *Window) location() (*time.Location, error) {
	if w.Zone == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(w.Zone)
	if err != nil {
		return nil, fmt.Errorf("governance: zone %q: %w", w.Zone, err)
	}
	return loc, nil
}
