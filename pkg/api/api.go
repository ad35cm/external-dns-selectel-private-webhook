package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ad35cm/external-dns-selectel-private-webhook/pkg/metrics"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	fiberlogger "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/pprof"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/provider"
)

const shutdownTimeout = 30 * time.Second

type Api interface {
	// Listen serves the webhook API on webhookAddress and the health and metrics endpoints on healthAddress
	// until a termination signal arrives or one of the servers fails.
	Listen(webhookAddress, healthAddress string) error
	// Test sends a request to the webhook API.
	Test(req *http.Request, msTimeout ...int) (resp *http.Response, err error)
	// TestHealth sends a request to the health and metrics server.
	TestHealth(req *http.Request, msTimeout ...int) (resp *http.Response, err error)
}

type api struct {
	logger  *zap.Logger
	webhook *fiber.App
	health  *fiber.App
}

func (a api) Test(req *http.Request, msTimeout ...int) (resp *http.Response, err error) {
	return a.webhook.Test(req, msTimeout...)
}

func (a api) TestHealth(req *http.Request, msTimeout ...int) (resp *http.Response, err error) {
	return a.health.Test(req, msTimeout...)
}

func (a api) Listen(webhookAddress, healthAddress string) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sigCh)

	serveErr := make(chan error, 2)
	serve := func(app *fiber.App, name, address string) {
		a.logger.Info("starting server", zap.String("server", name), zap.String("address", address))

		if err := app.Listen(address); err != nil {
			serveErr <- errors.Join(errors.New(name+" server stopped"), err)
		}
	}

	go serve(a.health, "health", healthAddress)
	go serve(a.webhook, "webhook", webhookAddress)

	var stopErr error

	select {
	case sig := <-sigCh:
		a.logger.Info("shutting down servers due to received signal", zap.String("signal", sig.String()))
	case stopErr = <-serveErr:
		a.logger.Error("shutting down servers due to server failure", zap.Error(stopErr))
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	err := errors.Join(stopErr, a.webhook.ShutdownWithContext(ctx), a.health.ShutdownWithContext(ctx))
	if err != nil {
		a.logger.Error("error shutting down servers", zap.Error(err))
	}

	return err
}

//go:generate mockgen -destination=./mock/api.go -source=./api.go Provider
type Provider interface {
	provider.Provider
}

func newApp() *fiber.App {
	return fiber.New(fiber.Config{
		DisableStartupMessage: true,
		JSONEncoder:           json.Marshal,
		JSONDecoder:           json.Unmarshal,
	})
}

// New returns the webhook API, which external-dns calls, and a separate health and metrics server for probes and
// scraping. Keeping them apart allows the webhook API to listen on localhost only.
func New(logger *zap.Logger, middlewareCollector metrics.HttpApiMetrics, provider provider.Provider) Api {
	health := newApp()
	registerAt(health, "/metrics")
	health.Get("/healthz", Health)

	app := newApp()
	app.Use(NewMetricsMiddleware(middlewareCollector))
	app.Use(fiberlogger.New())
	app.Use(pprof.New(pprof.Config{Prefix: "/pprof"}))
	app.Use(fiberrecover.New())
	app.Use(helmet.New())

	webhookRoutes := webhook{
		provider: provider,
		logger:   logger,
	}

	app.Get("/records", webhookRoutes.Records)
	app.Get("/", webhookRoutes.GetDomainFilter)
	app.Post("/records", webhookRoutes.ApplyChanges)
	app.Post("/adjustendpoints", webhookRoutes.AdjustEndpoints)

	return &api{
		logger:  logger,
		webhook: app,
		health:  health,
	}
}
