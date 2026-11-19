package fabric_test

import (
	"fmt"

	"github.com/gatework/fabric"
)

func ExampleDecodeIntent() {
	in, err := fabric.DecodeIntent([]byte(`schema: 1
regions:
  - name: dc1
    fabric: {asn: 65001, anycast_gateway_mac: 0000.2222.3333}
    numbering: {l2vni_base: 10000}
    switches:
      - {name: LEF01, mgmt: 198.51.100.11, role: leaf}
    tenants:
      - {name: PROD, l3vni: 50000, vlan: 999}
    networks:
      - {name: web, vlan: 100, tenant: PROD, gateway: 10.0.100.1/24}
`))
	if err != nil {
		fmt.Println(err)
		return
	}
	r, _ := in.Region("dc1")
	n, _ := r.Network(100)
	vni, _ := r.VNIOf(n)
	fmt.Println(n.Name, vni, n.Gateway)
	// Output: web 10100 10.0.100.1/24
}
