package selprovider

import (
	"strings"

	privatedns "github.com/selectel/private-dns-go/pkg/v1"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/provider"
)

const (
	actionCreate = "create"
	actionUpdate = "update"
	actionDelete = "delete"
)

// isSupportedRecordType reports whether Private DNS can store the record type.
func isSupportedRecordType(recordType string) bool {
	switch recordType {
	case endpoint.RecordTypeA, endpoint.RecordTypeAAAA, endpoint.RecordTypeCNAME,
		endpoint.RecordTypeMX, endpoint.RecordTypeTXT:
		return true
	default:
		return false
	}
}

// canonicalName returns the lower-case FQDN with a trailing dot, the form the Private DNS API stores.
func canonicalName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), ".")) + "."
}

// endpointFromRecord converts a Private DNS record set into an external-dns endpoint. A record set that inherits
// the zone TTL (ttl -1) becomes an endpoint without a configured TTL. It returns nil for unsupported types.
func endpointFromRecord(record *privatedns.Record) *endpoint.Endpoint {
	if !isSupportedRecordType(record.Type) {
		return nil
	}

	var ttl endpoint.TTL
	if record.TTL > 0 {
		ttl = endpoint.TTL(record.TTL)
	}

	// NewEndpointWithTTL drops the trailing dot of the name and of CNAME/MX targets, matching desired endpoints.
	return endpoint.NewEndpointWithTTL(record.Domain, record.Type, ttl, record.Values...)
}

// recordSetFromEndpoint converts an external-dns endpoint into a Private DNS record set. All targets of the endpoint
// become the values of a single record set. Endpoints without a configured TTL inherit the zone TTL.
func recordSetFromEndpoint(ep *endpoint.Endpoint) *privatedns.RecordSetDTO {
	values := make([]string, len(ep.Targets))
	for i, target := range ep.Targets {
		switch ep.RecordType {
		case endpoint.RecordTypeCNAME, endpoint.RecordTypeMX:
			// CNAME targets and MX exchanges ("10 mail.example.com") are FQDNs.
			values[i] = provider.EnsureTrailingDot(target)
		default:
			values[i] = target
		}
	}

	recordSet := &privatedns.RecordSetDTO{
		Type:   ep.RecordType,
		Domain: canonicalName(ep.DNSName),
		Values: values,
	}

	if ep.RecordTTL.IsConfigured() {
		ttl := int(ep.RecordTTL)
		recordSet.TTL = &ttl
	}

	return recordSet
}

// zoneForName returns the zone with the longest domain that contains name, or nil if no zone contains it.
func zoneForName(name string, zones []*privatedns.Zone) *privatedns.Zone {
	name = canonicalName(name)

	var (
		best       *privatedns.Zone
		bestLength int
	)

	for _, zone := range zones {
		zoneDomain := canonicalName(zone.Domain)
		if name != zoneDomain && !strings.HasSuffix(name, "."+zoneDomain) {
			continue
		}

		if len(zoneDomain) > bestLength {
			best = zone
			bestLength = len(zoneDomain)
		}
	}

	return best
}

// autoRecords indexes the record sets that Private DNS generates for the networks bound to a zone (shown as Auto
// in the control panel) by canonical domain. They belong to the network binding and are never changed here.
type autoRecords map[string][]string

func newAutoRecords(records []*privatedns.Record) autoRecords {
	auto := autoRecords{}

	for _, record := range records {
		if record.Generated {
			name := canonicalName(record.Domain)
			auto[name] = append(auto[name], record.Type)
		}
	}

	return auto
}

// conflict returns the type of the Auto record set that the change would modify, delete or collide with.
func (a autoRecords) conflict(action string, ep *endpoint.Endpoint) (string, bool) {
	for _, recordType := range a[canonicalName(ep.DNSName)] {
		if strings.EqualFold(recordType, ep.RecordType) {
			return recordType, true
		}

		// A CNAME cannot coexist with another record set of the same name, so writing one would fail the request.
		if action != actionDelete &&
			(ep.RecordType == endpoint.RecordTypeCNAME || recordType == endpoint.RecordTypeCNAME) {
			return recordType, true
		}
	}

	return "", false
}

// getLogFields returns the log fields describing a change.
func getLogFields(change *endpoint.Endpoint, action, zone string) []zap.Field {
	return []zap.Field{
		zap.String("record", change.DNSName),
		zap.String("content", strings.Join(change.Targets, ",")),
		zap.String("type", change.RecordType),
		zap.String("action", action),
		zap.String("zone", zone),
	}
}
