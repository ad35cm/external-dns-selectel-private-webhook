package selprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	mock_selprovider "github.com/ad35cm/external-dns-selectel-private-webhook/internal/selprovider/mock"
	privatedns "github.com/selectel/private-dns-go/pkg/v1"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
)

const testToken = "test-token"

// putRequest is a hard update request received by fakeAPI.
type putRequest struct {
	zoneID string
	body   privatedns.PutRecordsDTO
}

// fakeAPI is an in-memory Private DNS API. Like the real API it normalizes record domains and CNAME values to
// lower-case FQDNs, stores ttl -1 for record sets without TTL, and applies the deletions of a hard update before
// the record sets to set, atomically.
type fakeAPI struct {
	server *httptest.Server

	mu      sync.Mutex
	zones   []*privatedns.Zone
	records map[string][]*privatedns.Record
	puts    []putRequest

	// Status codes returned instead of a successful response, 0 means success.
	zonesStatus   int
	recordsStatus int
	putStatus     int
}

func newFakeAPI(t *testing.T, zones []*privatedns.Zone, records map[string][]*privatedns.Record) *fakeAPI {
	t.Helper()

	api := &fakeAPI{zones: zones, records: records}
	if api.records == nil {
		api.records = map[string][]*privatedns.Record{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /zones", api.listZones)
	mux.HandleFunc("GET /zones/{zoneID}/recordset", api.listRecords)
	mux.HandleFunc("PUT /zones/{zoneID}/recordset", api.putRecords)

	api.server = httptest.NewServer(api.authorized(mux))
	t.Cleanup(api.server.Close)

	return api
}

func (f *fakeAPI) authorized(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != testToken {
			writeError(w, http.StatusUnauthorized, "bad token")

			return
		}

		next.ServeHTTP(w, r)
	})
}

func (f *fakeAPI) listZones(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.zonesStatus != 0 {
		writeError(w, f.zonesStatus, "zones failure")

		return
	}

	writeJSON(w, map[string]any{"zones": f.zones})
}

func (f *fakeAPI) listRecords(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.recordsStatus != 0 {
		writeError(w, f.recordsStatus, "records failure")

		return
	}

	writeJSON(w, map[string]any{"records": f.records[r.PathValue("zoneID")]})
}

func (f *fakeAPI) putRecords(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var body privatedns.PutRecordsDTO
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())

		return
	}

	zoneID := r.PathValue("zoneID")
	f.puts = append(f.puts, putRequest{zoneID: zoneID, body: body})

	if f.putStatus != 0 {
		writeError(w, f.putStatus, "Record (bad.example.com. <A>) has invalid value")

		return
	}

	records := f.records[zoneID]
	for _, del := range body.Delete {
		records = slices.DeleteFunc(records, func(rec *privatedns.Record) bool {
			return rec.Type == del.Type && rec.Domain == canonicalName(del.Domain)
		})
	}

	for _, set := range body.Set {
		stored := &privatedns.Record{Type: set.Type, Domain: canonicalName(set.Domain), TTL: -1, Values: set.Values}
		if set.TTL != nil {
			stored.TTL = *set.TTL
		}

		if set.Type == endpoint.RecordTypeCNAME {
			stored.Values = []string{canonicalName(set.Values[0])}
		}

		records = slices.DeleteFunc(records, func(rec *privatedns.Record) bool {
			return rec.Type == stored.Type && rec.Domain == stored.Domain
		})
		records = append(records, stored)
	}

	f.records[zoneID] = records
	writeJSON(w, map[string]any{"records": records})
}

// state returns a copy of the record sets of a zone.
func (f *fakeAPI) state(zoneID string) []privatedns.Record {
	f.mu.Lock()
	defer f.mu.Unlock()

	result := make([]privatedns.Record, 0, len(f.records[zoneID]))
	for _, rec := range f.records[zoneID] {
		result = append(result, *rec)
	}

	slices.SortFunc(result, func(a, b privatedns.Record) int {
		return strings.Compare(a.Domain+" "+a.Type, b.Domain+" "+b.Type)
	})

	return result
}

func (f *fakeAPI) putRequests() []putRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.puts)
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": status, "message": message}})
}

// newTestProvider returns a provider for the fake API whose Keystone provider returns testToken tokenCalls times.
func newTestProvider(t *testing.T, api *fakeAPI, config Config, tokenCalls int) *Provider {
	t.Helper()

	ctrl := gomock.NewController(t)
	keystone := mock_selprovider.NewMockKeystoneProvider(ctrl)
	keystone.EXPECT().GetToken().Return(testToken, nil).Times(tokenCalls)

	config.BaseURL = api.server.URL
	config.KeystoneProvider = keystone

	p, err := New(config, zap.NewNop())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return p
}
