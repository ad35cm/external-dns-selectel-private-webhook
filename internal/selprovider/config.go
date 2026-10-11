package selprovider

import (
	"sigs.k8s.io/external-dns/endpoint"
)

// Config is used to configure the creation of the Provider.
type Config struct {
	// BaseURL is the Private DNS API endpoint of a pool, e.g. https://ru-6.cloud.api.selcloud.ru/private-dns/v1.
	BaseURL string
	// KeystoneProvider issues the project-scoped Keystone token sent in the X-Auth-Token header.
	KeystoneProvider KeystoneProvider
	// DomainFilter limits the managed zones. A nil or empty filter matches every zone of the project.
	DomainFilter *endpoint.DomainFilter
	// DryRun logs the changes instead of sending them to the API.
	DryRun bool
	// Workers is the number of zones processed concurrently.
	Workers int
}

//go:generate mockgen -destination=./mock/keystone_provider.go -source=./config.go KeystoneProvider
type KeystoneProvider interface {
	GetToken() (string, error)
}
