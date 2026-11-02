# sylva, when you have hardware

Span does not vendor sylva-core, Metal3, or Cluster API.

On a machine with KVM, or on real machines with a BMC, follow the current public sylva-core docs:

1. A temporary kind cluster.
2. Flux on that cluster.
3. The management cluster, then the pivot off kind.
4. Enable the upstream metal3 unit (`capm3`) in the sylva-units values. Do not paste that chart into this repo.
5. One workload cluster from a BareMetalHost, a control plane, and workers.
6. Flux installs the span operator and the helper on that workload cluster.
7. Apply one Segment whose prefix you own on that lab network.

`hack/sylva-lab.sh` refuses to start that path here. The helper self-test is the only kernel proof this laptop can run.
