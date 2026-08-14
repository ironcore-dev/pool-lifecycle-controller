# pool-lifecycle-controller
[![REUSE status](https://api.reuse.software/badge/github.com/ironcore-dev/pool-lifecycle-controller)](https://api.reuse.software/info/github.com/ironcore-dev/pool-lifecycle-controller)
[![GitHub License](https://img.shields.io/static/v1?label=License&message=Apache-2.0&color=blue)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://makeapullrequest.com)


## Description

`pool-lifecycle-controller` coordinates the deletion of [Cluster API](https://cluster-api.sigs.k8s.io/)
(CAPI) compute nodes with graceful eviction of the [IronCore](https://github.com/ironcore-dev/ironcore)
`Machines` running on them. When CAPI replaces or deletes a compute node (during upgrades,
scale-down, or remediation), the underlying node backs an IronCore `MachinePool`, and deleting
it while IronCore VMs are still running would drop those workloads.

> **Note:** This controller does not define any CRDs of its own. It reconciles the external CAPI
> `Machine` and IronCore `Machine`/`MachinePool` types, so there is nothing to `make install` and
> no `config/samples` to apply.


## Contributing

We'd love to get feedback from you. Please report bugs, suggestions or post questions by opening a GitHub issue.

## Licensing

Copyright 2025 SAP SE or an SAP affiliate company and IronCore contributors. Please see our [LICENSE](LICENSE) for
copyright and license information. Detailed information including third-party components and their licensing/copyright
information is available [via the REUSE tool](https://api.reuse.software/info/github.com/ironcore-dev/ironcore).

<p align="center"><img alt="Bundesministerium für Wirtschaft und Energie (BMWE)-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="400"/></p>
