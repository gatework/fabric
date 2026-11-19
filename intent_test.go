package fabric_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gatework/fabric"
)

func loadIntent(tb testing.TB) *fabric.Intent {
	tb.Helper()
	body, err := os.ReadFile("testdata/intent.yaml")
	if err != nil {
		tb.Fatal(err)
	}
	in, err := fabric.DecodeIntent(body)
	if err != nil {
		tb.Fatal(err)
	}
	return in
}

func TestDecodeIntent(t *testing.T) {
	t.Parallel()
	in := loadIntent(t)
	r, ok := in.Region("dc1")
	if !ok {
		t.Fatal("region dc1 missing")
	}
	if len(r.VTEPs()) != 3 || len(r.ByRole(fabric.RoleSpine)) != 1 {
		t.Errorf("roles: vteps %d", len(r.VTEPs()))
	}
	n, _ := r.Network(300)
	if vni, ok := r.VNIOf(n); !ok || vni != 10300 {
		t.Errorf("derived vni = %v %v", vni, ok)
	}
	l2, _ := r.Network(200)
	spine, _ := r.Switch("DC1-SPN01")
	border, _ := r.Switch("DC1-BDR01")
	leaf, _ := r.Switch("DC1-LEF01")
	if r.Carries(l2, border) || !r.Carries(l2, leaf) || r.Carries(n, spine) {
		t.Error("network placement wrong")
	}
	prod, _ := r.Tenant("PROD")
	if !r.Hosts(prod, border) || r.Hosts(prod, spine) {
		t.Error("tenant placement wrong")
	}
	body, err := fabric.EncodeIntent(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fabric.DecodeIntent(body); err != nil {
		t.Fatalf("round trip: %v\n%s", err, body)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	t.Parallel()
	in := loadIntent(t)
	r := in.Regions[0]
	r.Networks = append(r.Networks,
		&fabric.Network{VLAN: 100},
		&fabric.Network{VLAN: 400, VNI: 50000},
		&fabric.Network{VLAN: 401, Tenant: "NOPE"},
		&fabric.Network{VLAN: 402, Tenant: "PROD", Gateway: r.Networks[0].Gateway},
	)
	r.Switches[1].VPCPeer = "DC1-BDR01"
	err := in.Validate()
	var ve *fabric.ValidationError
	if !errors.As(err, &ve) || !errors.Is(err, fabric.ErrIntent) {
		t.Fatalf("err = %v", err)
	}
	text := ""
	for _, p := range ve.Problems {
		text += p.String() + "\n"
	}
	for _, want := range []string{"vlan already used", "is a tenant l3vni", "unknown tenant NOPE", "overlaps", "does not name"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing problem %q in:\n%s", want, text)
		}
	}
}

func TestDecodeIntentRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	if _, err := fabric.DecodeIntent([]byte("schema: 1\nregion: []\n")); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestValueParsers(t *testing.T) {
	t.Parallel()
	if _, err := fabric.ParseVLAN("4095"); err == nil {
		t.Error("vlan 4095 accepted")
	}
	if _, err := fabric.ParseVNI("16777215"); err == nil {
		t.Error("vni 2^24-1 accepted")
	}
	if a, err := fabric.ParseASN("1.10"); err != nil || a != 65546 {
		t.Errorf("asdot = %v %v", a, err)
	}
	if m, err := fabric.ParseMAC("00:00:22:22:33:33"); err != nil || m != "0000.2222.3333" {
		t.Errorf("mac = %v %v", m, err)
	}
}

func FuzzDecodeIntent(f *testing.F) {
	body, err := os.ReadFile("testdata/intent.yaml")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	f.Fuzz(func(t *testing.T, b []byte) {
		in, err := fabric.DecodeIntent(b)
		if err != nil {
			return
		}
		if in.Validate() != nil {
			t.Fatal("decoded intent fails validation")
		}
	})
}
