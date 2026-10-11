package cmd

import (
	"errors"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"

	"github.com/ad35cm/external-dns-selectel-private-webhook/internal/selprovider"
	"github.com/ad35cm/external-dns-selectel-private-webhook/pkg/api"
	"github.com/ad35cm/external-dns-selectel-private-webhook/pkg/keystone"
	"github.com/ad35cm/external-dns-selectel-private-webhook/pkg/metrics"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/external-dns/endpoint"
)

var (
	authorizationURL string
	accountID        string
	username         string
	password         string
	projectID        string
	apiPort          string
	healthPort       string
	pool             string
	baseURL          string
	worker           int
	domainFilter     []string
	dryRun           bool
	logLevel         string
)

const (
	// DefaultAuthURL represents default endpoint where keystone-token should be retrieved.
	DefaultAuthURL = "https://cloud.api.selcloud.ru/identity/v3"
	// PrivateDNSURLTemplate builds the Private DNS API endpoint of a pool.
	PrivateDNSURLTemplate = "https://%s.cloud.api.selcloud.ru/private-dns/v1"
	// webhookHost keeps the webhook API reachable from the external-dns container of the same pod only.
	webhookHost = "127.0.0.1"
)

var poolPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var rootCmd = &cobra.Command{
	Use:          "external-dns-selectel-private-webhook",
	Short:        "provider webhook for the Selectel Private DNS service",
	Long:         "provider webhook for the Selectel Private DNS service",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		privateDNSURL, err := getPrivateDNSURL()
		if err != nil {
			return err
		}

		if err := validateCredentials(); err != nil {
			return err
		}

		logger := getLogger()
		defer func(logger *zap.Logger) {
			err := logger.Sync()
			if err != nil {
				log.Printf("synchronization of logs failed with error: %v", err)
			}
		}(logger)

		logger.Info(
			"starting external-dns-selectel-private-webhook",
			zap.String("version", cmd.Root().Version),
			zap.String("private_dns_url", privateDNSURL),
			zap.Strings("domain_filter", domainFilter),
			zap.Bool("dry_run", dryRun),
		)

		keystoneProvider := keystone.NewProvider(logger, keystone.Credentials{
			IdentityEndpoint: authorizationURL,
			AccountID:        accountID,
			ProjectID:        projectID,
			Username:         username,
			Password:         password,
		})

		selProvider, err := selprovider.New(selprovider.Config{
			BaseURL:          privateDNSURL,
			KeystoneProvider: keystoneProvider,
			DomainFilter:     endpoint.NewDomainFilter(domainFilter),
			DryRun:           dryRun,
			Workers:          worker,
		}, logger.With(zap.String("component", "selprovider")))
		if err != nil {
			return err
		}

		app := api.New(logger.With(zap.String("component", "api")), metrics.NewHttpApiMetrics(), selProvider)

		return app.Listen(net.JoinHostPort(webhookHost, apiPort), net.JoinHostPort("", healthPort))
	},
}

// getPrivateDNSURL returns --base-url when set, otherwise the endpoint of --pool.
func getPrivateDNSURL() (string, error) {
	if baseURL != "" {
		return strings.TrimSuffix(baseURL, "/"), nil
	}

	if pool == "" {
		return "", errors.New("either --pool/POOL or --base-url/BASE_URL must be set")
	}

	if !poolPattern.MatchString(pool) {
		return "", fmt.Errorf("invalid pool %q, expected a pool name such as ru-6", pool)
	}

	return fmt.Sprintf(PrivateDNSURLTemplate, pool), nil
}

// validateCredentials fails fast when the Keystone credentials are incomplete.
func validateCredentials() error {
	required := []struct {
		flag  string
		value string
	}{
		{"project-id", projectID},
		{"account-id", accountID},
		{"username", username},
		{"password", password},
	}

	var missing []string

	for _, r := range required {
		if r.value == "" {
			missing = append(missing, "--"+r.flag+"/"+envName(r.flag))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}

	return nil
}

func envName(flag string) string {
	return strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

func getLogger() *zap.Logger {
	cfg := zap.Config{
		Level:    zap.NewAtomicLevelAt(getZapLogLevel()),
		Encoding: "json", // or "console"
		// ... other zap configuration as needed
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, errLogger := cfg.Build()
	if errLogger != nil {
		panic(errLogger)
	}

	return logger
}

func getZapLogLevel() zapcore.Level {
	switch logLevel {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// Execute runs the webhook. version is reported by --version and in the startup log.
func Execute(version string) error {
	rootCmd.Version = version

	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&apiPort, "api-port", "8888", "Specifies the port of the webhook API. "+
		"It listens on 127.0.0.1 only, external-dns calls it from the same pod.")
	rootCmd.PersistentFlags().StringVar(&healthPort, "health-port", "8080", "Specifies the port of the "+
		"/healthz and /metrics endpoints. It listens on all interfaces.")
	rootCmd.PersistentFlags().StringVar(&pool, "pool", "", "Specifies the Selectel pool of the private zones, "+
		"e.g. ru-6. The API endpoint is https://<pool>.cloud.api.selcloud.ru/private-dns/v1.")
	rootCmd.PersistentFlags().StringVar(&baseURL, "base-url", "", "Overrides the Private DNS API endpoint "+
		"derived from --pool.")
	rootCmd.PersistentFlags().StringVar(&projectID, "project-id", "", "Specifies the project id to authorize.")
	rootCmd.PersistentFlags().StringVar(&accountID, "account-id", "", "Specifies the account id to authorize.")
	rootCmd.PersistentFlags().StringVar(&authorizationURL, "auth-url", DefaultAuthURL, "Identifies the URL for utilizing the API to receive keystone-token.")
	rootCmd.PersistentFlags().StringVar(&username, "username", "", "Specifies the username of service user to authorize.")
	rootCmd.PersistentFlags().StringVar(&password, "password", "", "Specifies the password of service user to authorize.")
	rootCmd.PersistentFlags().IntVar(&worker, "worker", 10, "Specifies the number "+
		"of zones processed concurrently. It is important to avoid setting this number "+
		"excessively high to prevent receiving 429 rate limiting from the API.")
	rootCmd.PersistentFlags().StringSliceVar(&domainFilter, "domain-filter", []string{}, "Establishes a filter for "+
		"DNS zone names. Repeat the flag or separate domains with commas.")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Specifies whether to perform a dry run.")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Specifies the log level. Possible values are: debug, info, warn, error.")
}

func initConfig() {
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	// There is some issue, where the integration of Cobra with Viper will result in wrong values, therefore we are
	// setting the values from viper manually. The issue is, that with the standard integration, viper will see, that
	// Cobra parameters are set - even if the command line parameter was not used and the default value was set. But
	// when Viper notices that the value is set, it will not overwrite the default value with the environment variable.
	// Another possibility would be to not have any default values set for cobra command line parameters, but this would
	// break the automatic help output from the cli. The manual way here seems the best solution for now.
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Changed && viper.IsSet(f.Name) {
			if err := rootCmd.PersistentFlags().Set(f.Name, fmt.Sprint(viper.Get(f.Name))); err != nil {
				log.Fatalf("unable to set value for command line parameter: %v", err)
			}
		}
	})
}
