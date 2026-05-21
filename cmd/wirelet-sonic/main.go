// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/sonic"
	"github.com/ironcore-dev/wire/wirelet/cli"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	var flags cli.Flags
	var name string
	var interfaces []string

	flags.AddFlags(pflag.CommandLine)
	pflag.StringVar(&name, "name", name, "Name of the device")
	pflag.StringSliceVarP(&interfaces, "interface", "I", interfaces, "Names of the interfaces to reconcile")

	pflag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&flags.ZapOptions)))
	setupLog := ctrl.Log.WithName("setup")

	if name == "" {
		setupLog.Error(nil, "Must specify --name")
		os.Exit(1)
	}

	interfaceSet := sets.New(interfaces...)

	prov, err := sonic.NewRuntime()
	if err != nil {
		setupLog.Error(err, "Error creating sonic runtime")
		os.Exit(1)
	}

	if err := cli.Run(ctrl.SetupSignalHandler(), prov, cli.Options{
		Flags: &flags,
		DevicePredicate: func(device *v1alpha1.Device) bool {
			return device.Name == name
		},
		DeviceInterfacePredicate: func(iface *v1alpha1.DeviceInterface) bool {
			return interfaceSet.Has(iface.Name)
		},
	}); err != nil {
		setupLog.Error(err, "Error running wirelet")
		os.Exit(1)
	}
}
