# fabric
[![REUSE status](https://api.reuse.software/badge/github.com/ironcore-dev/fabric)](https://api.reuse.software/info/github.com/ironcore-dev/fabric)
[![GitHub License](https://img.shields.io/static/v1?label=License&message=Apache-2.0&color=blue)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://makeapullrequest.com)

`fabric` enables declarative configuration of network nodes.

## Description

`fabric` models unconfigured nodes as a `Node`. A `Node`
can have multiple `Interface`s. To actually make a
`Node` configured, a `Cell` resource is created referencing
the `Node` to configure (think of k8s' `Node` <-> `Pod` binding
with `Node` being a k8s `Node` and `Pod` being a `Cell`
resource, just that only a single `Cell` can be on a
`Node` at a time).

Since configuring a `Node` is heavily vendor-dependent, the
[`cellruntime/Runtime`](cellruntime/cellruntime.go) interface
allows a provider to implement the functionality required to
integrate with `fabric`.

## Getting Started

### Prerequisites

- The Go version declared in [`go.mod`](go.mod)
- Docker with BuildKit support
- `kubectl` and access to a compatible Kubernetes cluster

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/fabric:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/fabric:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/fabric:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/fabric/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing

Contributions are welcome. See the
[IronCore contributor guide](https://github.com/ironcore-dev/community/blob/main/docs/contributing.md) for the shared
contribution process.

**NOTE:** Run `make help` for more information on all available development and deployment targets.

## Licensing

Copyright SAP SE or an SAP affiliate company and IronCore contributors. Please see our [LICENSE](LICENSE) for
copyright and license information. Detailed information including third-party components and their licensing/copyright
information is available through the [REUSE metadata](REUSE.toml).

<p align="center"><img alt="Bundesministerium für Wirtschaft und Energie (BMWE)-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="400"/></p>
