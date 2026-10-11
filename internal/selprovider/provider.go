package selprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ad35cm/external-dns-selectel-private-webhook/pkg/httpdefault"
	privatedns "github.com/selectel/private-dns-go/pkg/v1"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/provider"
)

var (
	errEmptyBaseURL          = errors.New("private DNS base URL is empty")
	errEmptyKeystoneProvider = errors.New("keystone provider is nil")
)

// Provider implements the external-dns provider interface for Selectel Private DNS.
type Provider struct {
	provider.BaseProvider
	domainFilter     *endpoint.DomainFilter
	keystoneProvider KeystoneProvider
	baseURL          string
	httpClient       *http.Client
	dryRun           bool
	workers          int
	logger           *zap.Logger
}

// New creates a new Selectel Private DNS provider.
func New(config Config, logger *zap.Logger) (*Provider, error) {
	if config.BaseURL == "" {
		return nil, errEmptyBaseURL
	}

	if config.KeystoneProvider == nil {
		return nil, errEmptyKeystoneProvider
	}

	domainFilter := config.DomainFilter
	if domainFilter == nil {
		domainFilter = endpoint.NewDomainFilter(nil)
	}

	httpClient := httpdefault.Client()

	return &Provider{
		domainFilter:     domainFilter,
		keystoneProvider: config.KeystoneProvider,
		baseURL:          config.BaseURL,
		httpClient:       &httpClient,
		dryRun:           config.DryRun,
		workers:          max(config.Workers, 1),
		logger:           logger,
	}, nil
}

// GetDomainFilter returns the domain filter that external-dns applies to the endpoints of this provider.
func (p *Provider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}

// newClient returns a Private DNS client authorized with a fresh project-scoped Keystone token.
func (p *Provider) newClient() (*privatedns.PrivateDNSClient, error) {
	token, err := p.keystoneProvider.GetToken()
	if err != nil {
		p.logger.Error("failed to get keystone token", zap.Error(err))

		return nil, err
	}

	return privatedns.NewPrivateDNSClient(&privatedns.Config{
		URL:        p.baseURL,
		AuthToken:  token,
		HTTPClient: p.httpClient,
		UserAgent:  httpdefault.UserAgent,
	}), nil
}

// managedZones returns the project zones that match the domain filter. Zones reserved by another Selectel
// service (for example Managed Databases) cannot be modified and are skipped.
func (p *Provider) managedZones(ctx context.Context, client *privatedns.PrivateDNSClient) ([]*privatedns.Zone, error) {
	zones, err := client.ListZones(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}

	managed := make([]*privatedns.Zone, 0, len(zones))
	domains := make([]string, 0, len(zones))

	for _, zone := range zones {
		switch {
		case zone.ReservedBy != "":
			p.logger.Debug(
				"skipping zone reserved by another service",
				zap.String("zone", zone.Domain),
				zap.String("reserved_by", zone.ReservedBy),
			)
		case !p.domainFilter.Match(zone.Domain):
			p.logger.Debug("skipping zone outside the domain filter", zap.String("zone", zone.Domain))
		default:
			managed = append(managed, zone)
			domains = append(domains, zone.Domain)
		}
	}

	p.logger.Debug("managed zones", zap.Strings("zones", domains))

	return managed, nil
}
