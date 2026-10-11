package selprovider

import (
	"testing"

	privatedns "github.com/selectel/private-dns-go/pkg/v1"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/external-dns/endpoint"
)

func intPtr(v int) *int { return &v }

func TestEndpointFromRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record *privatedns.Record
		want   *endpoint.Endpoint
	}{
		{
			name:   "record set inheriting the zone TTL has no configured TTL",
			record: &privatedns.Record{Type: "A", Domain: "app.zone.test.", TTL: -1, Values: []string{"10.0.0.1"}},
			want:   endpoint.NewEndpoint("app.zone.test", "A", "10.0.0.1"),
		},
		{
			name:   "all values become targets of one endpoint",
			record: &privatedns.Record{Type: "AAAA", Domain: "app.zone.test.", TTL: 300, Values: []string{"fd00::1", "fd00::2"}},
			want:   endpoint.NewEndpointWithTTL("app.zone.test", "AAAA", 300, "fd00::1", "fd00::2"),
		},
		{
			name:   "CNAME target loses the trailing dot",
			record: &privatedns.Record{Type: "CNAME", Domain: "www.zone.test.", TTL: -1, Values: []string{"app.example.com."}},
			want:   endpoint.NewEndpoint("www.zone.test", "CNAME", "app.example.com"),
		},
		{
			name:   "MX exchange loses the trailing dot",
			record: &privatedns.Record{Type: "MX", Domain: "zone.test.", TTL: -1, Values: []string{"10 mail.zone.test."}},
			want:   endpoint.NewEndpoint("zone.test", "MX", "10 mail.zone.test"),
		},
		{
			name: "TXT value is kept verbatim",
			record: &privatedns.Record{Type: "TXT", Domain: "a-app.zone.test.", TTL: -1, Values: []string{
				`"heritage=external-dns,external-dns/owner=prod,external-dns/resource=service/ns/app"`,
			}},
			want: endpoint.NewEndpoint("a-app.zone.test", "TXT",
				`"heritage=external-dns,external-dns/owner=prod,external-dns/resource=service/ns/app"`),
		},
		{
			name:   "unsupported record type is dropped",
			record: &privatedns.Record{Type: "SOA", Domain: "zone.test.", TTL: -1, Values: []string{"ns1.zone.test."}},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, endpointFromRecord(tt.record))
		})
	}
}

func TestRecordSetFromEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ep   *endpoint.Endpoint
		want *privatedns.RecordSetDTO
	}{
		{
			name: "endpoint without TTL inherits the zone TTL",
			ep:   endpoint.NewEndpoint("App.Zone.test", "A", "10.0.0.1", "10.0.0.2"),
			want: &privatedns.RecordSetDTO{Type: "A", Domain: "app.zone.test.", Values: []string{"10.0.0.1", "10.0.0.2"}},
		},
		{
			name: "configured TTL is sent",
			ep:   endpoint.NewEndpointWithTTL("app.zone.test", "A", 60, "10.0.0.1"),
			want: &privatedns.RecordSetDTO{Type: "A", Domain: "app.zone.test.", TTL: intPtr(60), Values: []string{"10.0.0.1"}},
		},
		{
			name: "CNAME target becomes an FQDN",
			ep:   endpoint.NewEndpoint("www.zone.test", "CNAME", "app.example.com"),
			want: &privatedns.RecordSetDTO{Type: "CNAME", Domain: "www.zone.test.", Values: []string{"app.example.com."}},
		},
		{
			name: "MX exchange becomes an FQDN",
			ep:   endpoint.NewEndpoint("zone.test", "MX", "10 mail.zone.test"),
			want: &privatedns.RecordSetDTO{Type: "MX", Domain: "zone.test.", Values: []string{"10 mail.zone.test."}},
		},
		{
			name: "TXT value is sent verbatim",
			ep:   endpoint.NewEndpoint("a-app.zone.test", "TXT", `"heritage=external-dns"`, "ends.with.dot."),
			want: &privatedns.RecordSetDTO{Type: "TXT", Domain: "a-app.zone.test.", Values: []string{`"heritage=external-dns"`, "ends.with.dot."}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, recordSetFromEndpoint(tt.ep))
		})
	}
}

// TestRecordSetRoundTrip checks that an endpoint written through the API reads back unchanged. Any difference would
// make external-dns plan the same update on every sync.
func TestRecordSetRoundTrip(t *testing.T) {
	t.Parallel()

	zones := []*privatedns.Zone{{ID: "z1", Domain: "zone.test."}}
	endpoints := []*endpoint.Endpoint{
		endpoint.NewEndpoint("app.zone.test", "A", "10.0.0.1", "10.0.0.2"),
		endpoint.NewEndpointWithTTL("v6.zone.test", "AAAA", 120, "fd00::1"),
		endpoint.NewEndpoint("www.zone.test", "CNAME", "app.example.com"),
		endpoint.NewEndpoint("zone.test", "MX", "10 mail.zone.test"),
		endpoint.NewEndpoint("a-app.zone.test", "TXT",
			`"heritage=external-dns,external-dns/owner=prod,external-dns/resource=service/ns/app"`),
	}

	api := newFakeAPI(t, zones, nil)
	p := newTestProvider(t, api, Config{}, 2)

	err := p.ApplyChanges(t.Context(), planWithCreate(endpoints...))
	assert.NoError(t, err)

	got, err := p.Records(t.Context())
	assert.NoError(t, err)
	assert.ElementsMatch(t, endpoints, got)
}

func TestZoneForName(t *testing.T) {
	t.Parallel()

	parent := &privatedns.Zone{ID: "parent", Domain: "example.com."}
	child := &privatedns.Zone{ID: "child", Domain: "prod.example.com."}
	zones := []*privatedns.Zone{parent, child}

	tests := []struct {
		name     string
		dnsName  string
		wantZone *privatedns.Zone
	}{
		{"longest matching zone wins", "app.prod.example.com", child},
		{"zone apex", "prod.example.com.", child},
		{"parent zone", "app.example.com", parent},
		{"case insensitive", "APP.Prod.Example.COM", child},
		{"suffix must be on a label boundary", "app.notprod.example.com", parent},
		{"name outside all zones", "app.example.org", nil},
		{"name sharing a suffix with a zone", "badexample.com", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Same(t, tt.wantZone, zoneForName(tt.dnsName, zones))
		})
	}
}

func TestAutoRecordsConflict(t *testing.T) {
	t.Parallel()

	auto := newAutoRecords([]*privatedns.Record{
		{Type: "A", Domain: "vm-1.zone.test.", Values: []string{"192.168.0.10"}, Generated: true},
		{Type: "A", Domain: "manual.zone.test.", Values: []string{"10.0.0.1"}},
	})

	tests := []struct {
		name     string
		action   string
		ep       *endpoint.Endpoint
		conflict bool
	}{
		{"update of an Auto record set", actionUpdate, endpoint.NewEndpoint("vm-1.zone.test", "A", "10.0.0.9"), true},
		{"delete of an Auto record set", actionDelete, endpoint.NewEndpoint("VM-1.zone.test.", "A", "192.168.0.10"), true},
		{"create of a CNAME over an Auto record set", actionCreate, endpoint.NewEndpoint("vm-1.zone.test", "CNAME", "x.test"), true},
		{"delete of a CNAME next to an Auto record set", actionDelete, endpoint.NewEndpoint("vm-1.zone.test", "CNAME", "x.test"), false},
		{"TXT next to an Auto record set", actionCreate, endpoint.NewEndpoint("vm-1.zone.test", "TXT", `"heritage=external-dns"`), false},
		{"record set that is not generated", actionDelete, endpoint.NewEndpoint("manual.zone.test", "A", "10.0.0.1"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, conflict := auto.conflict(tt.action, tt.ep)
			assert.Equal(t, tt.conflict, conflict)
		})
	}
}
