// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/ironcore-dev/fabric/api/v1alpha1"
	"github.com/ironcore-dev/fabric/cellruntime"
	"github.com/ironcore-dev/fabric/fabriclet/cli"
	"github.com/ironcore-dev/fabric/sonic"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	var flags cli.Flags
	var name, backend, role, region, ipv6Base, searchDomain string
	var interfaces []string

	flags.AddFlags(pflag.CommandLine)
	pflag.StringVar(&name, "name", name, "Name of the node")
	pflag.StringVar(&backend, "backend", "script", "Runtime implementation to use (script, configdb)")
	pflag.StringVar(&role, "role", role, "Switch role (inband-leaf, inband-spine)")
	pflag.StringVar(&region, "region", region, "Region of the switch")
	pflag.StringVar(&ipv6Base, "ipv6-base", ipv6Base, "IPv6 base prefix of the site (e.g. 2001:db8:f00)")
	pflag.StringVar(&searchDomain, "search-domain", searchDomain, "Search domain of the switch")
	pflag.StringSliceVarP(&interfaces, "interface", "I", interfaces, "Names of the interfaces to reconcile")

	pflag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&flags.ZapOptions)))
	setupLog := ctrl.Log.WithName("setup")

	if name == "" {
		setupLog.Error(nil, "Must specify --name")
		os.Exit(1)
	}

	interfaceSet := sets.New(interfaces...)

	var prov cellruntime.Runtime
	var err error
	switch backend {
	case "script":
		prov, err = sonic.NewScriptRuntime(role, region, ipv6Base, searchDomain)
	case "configdb":
		prov, err = sonic.NewConfigDBRuntime(role, region, ipv6Base, searchDomain)
	default:
		err = fmt.Errorf("unknown backend %q", backend)
	}
	if err != nil {
		setupLog.Error(err, "Error creating sonic runtime")
		os.Exit(1)
	}

	if err := cli.Run(ctrl.SetupSignalHandler(), prov, cli.Options{
		Flags: &flags,
		Registration: &cli.RegistrationOptions{
			NodeName:   name,
			Interfaces: interfaces,
		},
		NodePredicate: func(node *v1alpha1.Node) bool {
			return node.Name == name
		},
		InterfacePredicate: func(iface *v1alpha1.Interface) bool {
			return interfaceSet.Has(iface.Name)
		},
	}); err != nil {
		setupLog.Error(err, "Error running fabriclet")
		os.Exit(1)
	}
}
