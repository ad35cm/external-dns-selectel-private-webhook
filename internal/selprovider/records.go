package selprovider

import (
	"context"
	"fmt"

	privatedns "github.com/selectel/private-dns-go/pkg/v1"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/external-dns/endpoint"
)

// Records returns the record sets of all managed zones, including Auto record sets. external-dns needs to see Auto
// record sets to avoid planning conflicting records; without an owner they are never planned for update or deletion.
func (p *Provider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	client, err := p.newClient()
	if err != nil {
		return nil, err
	}

	zones, err := p.managedZones(ctx, client)
	if err != nil {
		return nil, err
	}

	recordSets, err := p.listRecordSets(ctx, client, zones)
	if err != nil {
		return nil, err
	}

	var endpoints []*endpoint.Endpoint

	for _, records := range recordSets {
		for _, record := range records {
			if ep := endpointFromRecord(record); ep != nil {
				endpoints = append(endpoints, ep)
			}
		}
	}

	return endpoints, nil
}

// listRecordSets fetches the record sets of the zones concurrently. The result is indexed like zones.
func (p *Provider) listRecordSets(
	ctx context.Context,
	client *privatedns.PrivateDNSClient,
	zones []*privatedns.Zone,
) ([][]*privatedns.Record, error) {
	result := make([][]*privatedns.Record, len(zones))

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(p.workers)

	for i, zone := range zones {
		group.Go(func() error {
			records, err := client.ListRecords(groupCtx, zone.ID, nil)
			if err != nil {
				return fmt.Errorf("list record sets of zone %s: %w", zone.Domain, err)
			}

			result[i] = records

			return nil
		})
	}

	return result, group.Wait()
}
