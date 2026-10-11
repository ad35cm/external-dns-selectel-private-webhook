package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ad35cm/external-dns-selectel-private-webhook/pkg/api"
	mock_provider "github.com/ad35cm/external-dns-selectel-private-webhook/pkg/api/mock"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
)

func TestWebhook_DomainFilter(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockLogger := zap.NewNop()
	mockProvider := mock_provider.NewMockProvider(ctrl)
	mockMetricsCollector := getTestMockMetricsCollector(ctrl)
	expectedDomainFilter := endpoint.NewDomainFilter([]string{"prod-ru-6.burnsdev.com"})

	app := api.New(mockLogger, mockMetricsCollector, mockProvider)
	mockProvider.EXPECT().
		GetDomainFilter().
		Return(expectedDomainFilter).
		Times(1)

	req := httptest.NewRequest(http.MethodGet, "/", bytes.NewReader(nil))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var domainFilterResponse endpoint.DomainFilter
	err = json.NewDecoder(resp.Body).Decode(&domainFilterResponse)
	assert.NoError(t, err)
	assert.Equal(t, expectedDomainFilter, &domainFilterResponse)
}
