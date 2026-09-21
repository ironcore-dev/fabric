// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/ironcore-dev/fabric/api/v1alpha1"
	"github.com/ironcore-dev/fabric/fabriclet/cli"
	"github.com/ironcore-dev/fabric/sonic"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	var flags cli.Flags
	var name string
	var interfaces []string
	var sonicAddr string

	flags.AddFlags(pflag.CommandLine)
	pflag.StringVar(&name, "name", name, "Name of the node")
	pflag.StringSliceVarP(&interfaces, "interface", "I", interfaces, "Names of the interfaces to reconcile")
	pflag.StringVar(&sonicAddr, "sonic-addr", "localhost:50051", "Address of the sonic-agent gRPC endpoint")

	pflag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&flags.ZapOptions)))
	setupLog := ctrl.Log.WithName("setup")

	if name == "" {
		setupLog.Error(nil, "Must specify --name")
		os.Exit(1)
	}

	conn, err := grpc.NewClient(sonicAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		setupLog.Error(err, "Error connecting to sonic-agent")
		os.Exit(1)
	}
	defer conn.Close()

	interfaceSet := sets.New(interfaces...)

	prov, err := sonic.NewRuntime(conn)
	if err != nil {
		setupLog.Error(err, "Error creating sonic runtime")
		os.Exit(1)
	}

	if err := cli.Run(ctrl.SetupSignalHandler(), prov, cli.Options{
		Flags: &flags,
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
