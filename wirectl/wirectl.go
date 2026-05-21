package wirectl

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/ironcore-dev/wire/api/v1alpha1/applyconfiguration/api/v1alpha1"
	"github.com/ironcore-dev/wire/wirectl/api"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const FieldOwner = client.FieldOwner("wire.ironcore.dev/wirectl")

func RenderTopology(cfg *api.Config) ([]runtime.ApplyConfiguration, error) {
	var (
		topo      = cfg.Topology
		applyCfgs []runtime.ApplyConfiguration
		seenNames = sets.New[string]()
	)

	nodeNames := make([]string, 0, len(topo.Nodes))
	nodeNames = slices.AppendSeq(nodeNames, maps.Keys(topo.Nodes))
	slices.Sort(nodeNames)
	for _, nodeName := range nodeNames {
		node := topo.Nodes[nodeName]
		if seenNames.Has(nodeName) {
			return nil, fmt.Errorf("duplicate name %q", nodeName)
		}

		switch node.Kind {
		case "Device":
			applyCfgs = append(applyCfgs,
				v1alpha1.Device(nodeName, "").WithSpec(v1alpha1.DeviceSpec()))
		case "Server":
			applyCfgs = append(applyCfgs,
				v1alpha1.Server(nodeName, "").WithSpec(v1alpha1.ServerSpec()))
		default:
			return nil, fmt.Errorf("unknown node kind %q", node.Kind)
		}
		seenNames.Insert(nodeName)

		interfaceNames := make([]string, 0, len(node.Interfaces))
		interfaceNames = slices.AppendSeq(interfaceNames, maps.Keys(node.Interfaces))
		slices.Sort(interfaceNames)
		for _, ifaceName := range interfaceNames {
			name := fmt.Sprintf("%s-%s", nodeName, ifaceName)
			if seenNames.Has(name) {
				return nil, fmt.Errorf("duplicate name %q", name)
			}

			switch node.Kind {
			case "Device":
				applyCfgs = append(applyCfgs,
					v1alpha1.DeviceInterface(name, "").
						WithSpec(v1alpha1.DeviceInterfaceSpec().
							WithDeviceRef(v1alpha1.LocalObjectReference().WithName(nodeName))))
			case "Server":
				applyCfgs = append(applyCfgs,
					v1alpha1.ServerInterface(name, "").
						WithSpec(v1alpha1.ServerInterfaceSpec().
							WithServerRef(v1alpha1.LocalObjectReference().WithName(nodeName))))
			}
			seenNames.Insert(name)
		}
	}

	for i, link := range topo.Links {
		if len(link.Endpoints) != 2 {
			return nil, fmt.Errorf("link endpoint %d does not have exactly two endpoints", i)
		}

		e1 := link.Endpoints[0]
		e2 := link.Endpoints[1]

		n1, ok := topo.Nodes[e1.Node]
		if !ok {
			return nil, fmt.Errorf("node %q not found", e1.Node)
		}
		n2, ok := topo.Nodes[e2.Node]
		if !ok {
			return nil, fmt.Errorf("node %q not found", e2.Node)
		}

		var e1Cfg *v1alpha1.LinkEndpointApplyConfiguration
		switch n1.Kind {
		case "Device":
			e1Cfg = v1alpha1.LinkEndpoint().
				WithDeviceInterfaceRef(v1alpha1.LocalObjectReference().WithName(e1.Node))
		case "Server":
			e1Cfg = v1alpha1.LinkEndpoint().
				WithServerInterfaceRef(v1alpha1.LocalObjectReference().WithName(e1.Node))
		}

		var e2Cfg *v1alpha1.LinkEndpointApplyConfiguration
		switch n2.Kind {
		case "Device":
			e2Cfg = v1alpha1.LinkEndpoint().
				WithDeviceInterfaceRef(v1alpha1.LocalObjectReference().WithName(e2.Node))
		case "Server":
			e2Cfg = v1alpha1.LinkEndpoint().
				WithServerInterfaceRef(v1alpha1.LocalObjectReference().WithName(e2.Node))
		}

		name := fmt.Sprintf("%s-%s--%s-%s", e1.Node, e1.Interface, e2.Node, e2.Interface)
		if seenNames.Has(name) {
			return nil, fmt.Errorf("duplicate name %q", name)
		}

		applyCfgs = append(applyCfgs, v1alpha1.Link(name, "").WithEndpoints(e1Cfg, e2Cfg))
		seenNames.Insert(name)
	}

	return applyCfgs, nil
}

func ApplyTopology(ctx context.Context, c client.Client, cfg *api.Config) error {
	applyCfgs, err := RenderTopology(cfg)
	if err != nil {
		return fmt.Errorf("rendering config: %w", err)
	}

	for _, applyCfg := range applyCfgs {
		if err := c.Apply(ctx, applyCfg, FieldOwner); err != nil {
			return err
		}
	}
	return nil
}
