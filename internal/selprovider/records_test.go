package selprovider

import (
	"errors"
	"net/http"
	"testing"

	mock_selprovider "github.com/ad35cm/external-dns-selectel-private-webhook/internal/selprovider/mock"
	privatedns "github.com/selectel/private-dns-go/pkg/v1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
)

func recordsTestAPI(t *testing.T) *fakeAPI {
	t.Helper()

	return newFakeAPI(t,
		[]*privatedns.Zone{
			{ID: "prod", Domain: "prod.example.com.", TTL: 3600},
			{ID: "dbaas", Domain: "db.example.com.", ReservedBy: "dbaas"},
			{ID: "other", Domain: "example.org."},
		},
		map[string][]*privatedns.Record{
			"prod": {
				{Type: "A", Domain: "vm-1.prod.example.com.", TTL: -1, Values: []string{"192.168.0.10"}, Generated: true},
				{Type: "A", Domain: "app.prod.example.com.", TTL: 300, Values: []string{"10.0.0.1"}},
				{Type: "TXT", Domain: "a-app.prod.example.com.", TTL: -1, Values: []string{`"heritage=external-dns"`}},
			},
			"dbaas": {{Type: "A", Domain: "pg.db.example.com.", TTL: -1, Values: []string{"10.0.1.1"}}},
			"other": {{Type: "A", Domain: "app.example.org.", TTL: -1, Values: []string{"10.0.2.1"}}},
		},
	)
}

func TestRecords(t *testing.T) {
	t.Parallel()

	api := recordsTestAPI(t)
	p := newTestProvider(t, api, Config{DomainFilter: endpoint.NewDomainFilter([]string{"example.com"})}, 1)

	got, err := p.Records(t.Context())
	assert.NoError(t, err)

	// The reserved zone and the zone outside the domain filter are not managed. Auto record sets are reported so
	// that external-dns plans around them.
	assert.ElementsMatch(t, []*endpoint.Endpoint{
		endpoint.NewEndpoint("vm-1.prod.example.com", "A", "192.168.0.10"),
		endpoint.NewEndpointWithTTL("app.prod.example.com", "A", 300, "10.0.0.1"),
		endpoint.NewEndpoint("a-app.prod.example.com", "TXT", `"heritage=external-dns"`),
	}, got)
}

func TestRecordsWithoutDomainFilterSkipsOnlyReservedZones(t *testing.T) {
	t.Parallel()

	api := recordsTestAPI(t)
	p := newTestProvider(t, api, Config{}, 1)

	got, err := p.Records(t.Context())
	assert.NoError(t, err)

	names := make([]string, 0, len(got))
	for _, ep := range got {
		names = append(names, ep.DNSName)
	}

	assert.ElementsMatch(t, []string{
		"vm-1.prod.example.com", "app.prod.example.com", "a-app.prod.example.com", "app.example.org",
	}, names)
}

func TestRecordsErrors(t *testing.T) {
	t.Parallel()

	t.Run("zones request fails", func(t *testing.T) {
		t.Parallel()

		api := recordsTestAPI(t)
		api.zonesStatus = http.StatusForbidden
		p := newTestProvider(t, api, Config{}, 1)

		_, err := p.Records(t.Context())
		assert.ErrorContains(t, err, "list zones")
	})

	t.Run("record sets request fails", func(t *testing.T) {
		t.Parallel()

		api := recordsTestAPI(t)
		api.recordsStatus = http.StatusInternalServerError
		p := newTestProvider(t, api, Config{}, 1)

		_, err := p.Records(t.Context())
		assert.ErrorContains(t, err, "list record sets of zone")
	})

	t.Run("keystone token cannot be issued", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		keystone := mock_selprovider.NewMockKeystoneProvider(ctrl)
		keystone.EXPECT().GetToken().Return("", errors.New("authorization failed")).Times(1)

		p, err := New(Config{BaseURL: "http://127.0.0.1:1", KeystoneProvider: keystone}, zap.NewNop())
		assert.NoError(t, err)

		_, err = p.Records(t.Context())
		assert.ErrorContains(t, err, "authorization failed")
	})
}
