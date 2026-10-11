package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/ad35cm/external-dns-selectel-private-webhook/pkg/api"
	mock_provider "github.com/ad35cm/external-dns-selectel-private-webhook/pkg/api/mock"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestApi(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockLogger := zap.NewNop()
	mockProvider := mock_provider.NewMockProvider(ctrl)
	mockMetricsCollector := getTestMockMetricsCollector(ctrl)

	app := api.New(mockLogger, mockMetricsCollector, mockProvider)

	t.Run("Health and metrics are served apart from the webhook API", func(t *testing.T) {
		t.Parallel()

		for _, path := range []string{"/healthz", "/metrics"} {
			resp, err := app.TestHealth(httptest.NewRequest(http.MethodGet, path, nil))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode, "health server %s", path)

			resp, err = app.Test(httptest.NewRequest(http.MethodGet, path, nil))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode, "webhook API %s", path)
		}

		for _, path := range []string{"/", "/records"} {
			resp, err := app.TestHealth(httptest.NewRequest(http.MethodGet, path, nil))
			assert.NoError(t, err)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode, "health server %s", path)
		}
	})

	t.Run("Listen", func(t *testing.T) {
		t.Parallel()

		done := make(chan error, 1)
		go func() {
			done <- app.Listen("127.0.0.1:0", "127.0.0.1:0")
		}()

		time.Sleep(time.Second) // Allow servers to start

		// Simulate SIGTERM signal to trigger shutdown
		process, _ := os.FindProcess(os.Getpid())
		err := process.Signal(syscall.SIGTERM)
		assert.NoError(t, err)

		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("Listen did not return after SIGTERM")
		}
	})
}
