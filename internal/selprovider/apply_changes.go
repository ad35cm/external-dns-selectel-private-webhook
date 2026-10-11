package selprovider

import (
	"context"
	"fmt"

	privatedns "github.com/selectel/private-dns-go/pkg/v1"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

// recordChange is a single endpoint change routed to a zone.
type recordChange struct {
	action   string
	endpoint *endpoint.Endpoint
}

// zoneChanges collects the changes of one zone. They are sent in one atomic hard update (PUT) request.
type zoneChanges struct {
	zone    *privatedns.Zone
	changes []recordChange
}

// ApplyChanges applies a given set of changes. Each zone receives one hard update request that replaces the record
// sets of created and updated endpoints and deletes the record sets of deleted endpoints. Changes that touch Auto
// record sets are skipped.
func (p *Provider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	if changes == nil || len(changes.Create)+len(changes.UpdateNew)+len(changes.Delete) == 0 {
		return nil
	}

	client, err := p.newClient()
	if err != nil {
		return err
	}

	zones, err := p.managedZones(ctx, client)
	if err != nil {
		return err
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(p.workers)

	for _, batch := range p.groupByZone(changes, zones) {
		group.Go(func() error {
			return p.applyZoneChanges(groupCtx, client, batch)
		})
	}

	return group.Wait()
}

// groupByZone routes every change to the managed zone with the longest matching domain. Changes outside all managed
// zones are skipped with a warning, like other external-dns providers do.
func (p *Provider) groupByZone(changes *plan.Changes, zones []*privatedns.Zone) []*zoneChanges {
	byZoneID := map[string]*zoneChanges{}

	var batches []*zoneChanges

	add := func(action string, endpoints []*endpoint.Endpoint) {
		for _, ep := range endpoints {
			zone := zoneForName(ep.DNSName, zones)
			if zone == nil {
				p.logger.Warn("skipping change outside of all managed zones", getLogFields(ep, action, "")...)

				continue
			}

			batch, ok := byZoneID[zone.ID]
			if !ok {
				batch = &zoneChanges{zone: zone}
				byZoneID[zone.ID] = batch
				batches = append(batches, batch)
			}

			batch.changes = append(batch.changes, recordChange{action: action, endpoint: ep})
		}
	}

	add(actionCreate, changes.Create)
	add(actionUpdate, changes.UpdateNew)
	add(actionDelete, changes.Delete)

	return batches
}

// applyZoneChanges sends the changes of one zone in a single hard update request. The current record sets are read
// first so that changes to Auto record sets are dropped before the request is built.
func (p *Provider) applyZoneChanges(ctx context.Context, client *privatedns.PrivateDNSClient, batch *zoneChanges) error {
	current, err := client.ListRecords(ctx, batch.zone.ID, nil)
	if err != nil {
		return fmt.Errorf("list record sets of zone %s: %w", batch.zone.Domain, err)
	}

	request := p.buildRequest(batch, newAutoRecords(current))
	if len(request.Set) == 0 && len(request.Delete) == 0 {
		return nil
	}

	zoneFields := []zap.Field{
		zap.String("zone", batch.zone.Domain),
		zap.Int("set", len(request.Set)),
		zap.Int("delete", len(request.Delete)),
	}

	if p.dryRun {
		p.logger.Info("dry run, skipping record sets update", zoneFields...)

		return nil
	}

	if _, err := client.PutRecords(ctx, batch.zone.ID, request); err != nil {
		p.logger.Error("error updating record sets", append(zoneFields, zap.Error(err))...)

		return fmt.Errorf("update record sets of zone %s: %w", batch.zone.Domain, err)
	}

	p.logger.Info("record sets updated", zoneFields...)

	return nil
}

// buildRequest converts the changes of a zone into a hard update request, dropping changes to Auto record sets.
func (p *Provider) buildRequest(batch *zoneChanges, auto autoRecords) *privatedns.PutRecordsDTO {
	request := &privatedns.PutRecordsDTO{}

	for _, change := range batch.changes {
		logFields := getLogFields(change.endpoint, change.action, batch.zone.Domain)

		if autoType, found := auto.conflict(change.action, change.endpoint); found {
			p.logger.Warn(
				"skipping change to an Auto record set managed by a network bound to the zone",
				append(logFields, zap.String("auto_record_type", autoType))...,
			)

			continue
		}

		p.logger.Info("record set change", logFields...)

		if change.action == actionDelete {
			request.Delete = append(request.Delete, &privatedns.RecordDeleteDTO{
				Type:   change.endpoint.RecordType,
				Domain: canonicalName(change.endpoint.DNSName),
			})
		} else {
			request.Set = append(request.Set, recordSetFromEndpoint(change.endpoint))
		}
	}

	return request
}
