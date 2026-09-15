// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"time"

	wirev1alpha1 "github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/cellruntime"
	internalctrl "github.com/ironcore-dev/wire/wirelet/internal/controller"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/lru"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

type Flags struct {
	MetricsAddr          string
	MetricsCertPath      string
	MetricsCertName      string
	MetricsCertKey       string
	WebhookCertPath      string
	WebhookCertName      string
	WebhookCertKey       string
	ProbeAddr            string
	EnableLeaderElection bool
	SecureMetrics        bool
	EnableHTTP2          bool
	TLSOpts              []func(*tls.Config)
	ZapOptions           zap.Options
}

func (o *Flags) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.MetricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	fs.StringVar(&o.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&o.EnableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	fs.BoolVar(&o.SecureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	fs.StringVar(&o.WebhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	fs.StringVar(&o.WebhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	fs.StringVar(&o.WebhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	fs.StringVar(&o.MetricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	fs.StringVar(&o.MetricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	fs.StringVar(&o.MetricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	fs.BoolVar(&o.EnableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")

	zapFlags := flag.NewFlagSet("", flag.ExitOnError)
	o.ZapOptions.BindFlags(zapFlags)
	fs.AddGoFlagSet(zapFlags)
}

type Options struct {
	*Flags

	LeaderElectionID   string
	NodePredicate      func(*wirev1alpha1.Node) bool
	InterfacePredicate func(*wirev1alpha1.Interface) bool
}

type InitRuntimeFunc func() (rt cellruntime.Runtime)

func Run(
	ctx context.Context,
	prov cellruntime.Runtime,
	opts Options,
) error {
	setupLog := ctrl.Log.WithName("setup")

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("adding client-go to scheme: %w", err)
	}

	if err := wirev1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("adding wire to scheme: %w", err)
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !opts.EnableHTTP2 {
		opts.TLSOpts = append(opts.TLSOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := opts.TLSOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(opts.WebhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", opts.WebhookCertPath,
			"webhook-cert-name", opts.WebhookCertName,
			"webhook-cert-key", opts.WebhookCertKey)

		webhookServerOptions.CertDir = opts.WebhookCertPath
		webhookServerOptions.CertName = opts.WebhookCertName
		webhookServerOptions.KeyName = opts.WebhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   opts.MetricsAddr,
		SecureServing: opts.SecureMetrics,
		TLSOpts:       opts.TLSOpts,
	}

	if opts.SecureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(opts.MetricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", opts.MetricsCertPath,
			"metrics-cert-name", opts.MetricsCertName,
			"metrics-cert-key", opts.MetricsCertKey)

		metricsServerOptions.CertDir = opts.MetricsCertPath
		metricsServerOptions.CertName = opts.MetricsCertName
		metricsServerOptions.KeyName = opts.MetricsCertKey
	}

	cfg := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: opts.ProbeAddr,
		LeaderElection:         opts.EnableLeaderElection,
		LeaderElectionID:       opts.LeaderElectionID,
	})
	if err != nil {
		return fmt.Errorf("create new manager: %w", err)
	}

	if err := (&internalctrl.NodeReconciler{
		Client:                           mgr.GetClient(),
		APIReader:                        mgr.GetAPIReader(),
		CellRuntime:                      prov,
		NodePredicate:                    opts.NodePredicate,
		AbsenceCache:                     lru.New(500),
		CellRuntimePollImmediateInterval: 50 * time.Millisecond,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setting up node controller: %w", err)
	}

	if err := (&internalctrl.InterfaceReconciler{
		Client:             mgr.GetClient(),
		CellRuntime:        prov,
		InterfacePredicate: opts.InterfacePredicate,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setting up interface controller: %w", err)
	}

	if err := (&internalctrl.CellReconciler{
		Client:                           mgr.GetClient(),
		EventRecorder:                    mgr.GetEventRecorder("cell-controller"),
		NodePredicate:                    opts.NodePredicate,
		CellRuntime:                      prov,
		CellRuntimePollInterval:          1 * time.Minute,
		CellRuntimePollImmediateInterval: 50 * time.Millisecond,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setting up cell controller: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("adding healthz check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("adding readyz check: %w", err)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("starting manager: %w", err)
	}
	return nil
}
