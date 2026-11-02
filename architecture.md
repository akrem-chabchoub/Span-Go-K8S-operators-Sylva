# span architecture

Span is a network control plane for one promise: this cluster owns this prefix, and no other cluster does. The promise is a Kubernetes object called `Segment`. A Go operator judges it. A Rust helper, in a later phase, programs the kernel. Public Sylva installs the machines. Public kcp, in a later phase, is the shared front desk for the promise. Flux is how git stays in charge.

`learning.md` teaches each technology on its own, with questions. This file is how they are wired, and which outage each wire is there to stop.

## the problem, in a building

Think of a campus. Each building has a block of street addresses. If two buildings paint the same numbers on their doors, the ambulance goes to the wrong door. People then blame the ambulance.

That happens in real platforms, without any drama in the design:

- The address plan is a spreadsheet. Two teams copy it, each picks `10.20.30.0/24`, and both are "sure they checked."
- A gateway is typed as `10.20.31.1` into a `10.20.30.0/24` by a tired human. The mistake is stored as if it were a decision.
- Someone types `10.0.0.0/4` or worse, and one object now overlaps half the private network.
- A bare-metal node is reimaged because the disk failed or the OS must be patched by reinstall. The extra address lived in a shell history. The node joins the cluster and the address is gone. Apps fail. The cluster looks healthy.
- An incident responder adds a route with `ip route add` and does not delete it. The next incident is "ghost routing."
- The spreadsheet's owner leaves the team. The history leaves with them.
- Changing the address means rebuilding the machine, because cloud-init baked it in at first boot. A rebuild of bare metal is many minutes and a full wipe.
- To apply a prefix you need root. So the person who plans addresses is the person who can do anything to the host.
- Site A and site B each keep a copy. They drift. Traffic between them is debugged as a product bug.

Span is the campus assigning addresses as a form the building keeps enforcing. It is not a new way to install machines, and it is not a second spreadsheet with extra steps.

## what span is responsible for

Span owns the life of a prefix after a cluster exists:

- accept or reject the promise at the door
- make one winner when two promises overlap
- remember the promise in a way a user cannot silently retarget
- produce an explicit list of kernel operations
- later, apply that list and report what the kernel actually has
- remove the address when the promise is deleted

## what span leaves to other tools

| tool | the job it already has | what span does not redo |
| --- | --- | --- |
| Kubernetes API | store objects, auth, schema | a private database of prefixes |
| Sylva | recipe for a management cluster and workload clusters, reconciled by Flux | a fork of sylva-core in this repo |
| Cluster API | "this cluster should exist, with this many machines" | a second cluster provisioner |
| Metal3 / CAPM3 | boot a blank machine through its BMC and install an OS | IPMI scripts |
| Flux | make the cluster match git | a one-off installer script as the long-term path |
| Argo CD | the same GitOps idea, different controller | running it beside Flux on one management cluster |
| kcp | many workspaces, one published API | provisioning hardware |
| the kernel | addresses and routes that are actually in force | treating status as truer than `ip addr` |

The boundary is the product. Sylva answers "does the machine have an OS and a kubelet?" Span answers "which prefix is on it, and is that prefix unique?" Mixing those answers into one object is how a prefix change turns into a reinstall.

## business value, one problem at a time

### two teams, one subnet

Packets do not have namespaces. `10.20.30.0/24` in namespace `team-a` and the same prefix in `team-b` is one outage with two owners.

The webhook denies the second create when the first is already stored. If both creates pass the door in the same instant, the controller lists the API server (not its cache) and keeps a single winner. The older `creationTimestamp` wins. If the clock ties, `namespace/name` in alphabetical order wins. The loser gets `Ready=False`, reason `Overlap`, and an empty plan.

Empty plan matters. Phase 2 applies the plan. A loser with a leftover plan would still program the kernel. Refusing the promise means refusing the operations.

Value: the failure shows up as an API error or a condition, with the other object's name in the message. People stop debugging the application.

### a gateway that is not in the block

`10.20.30.0/24` does not contain `10.20.31.1`. The planner rejects it. The webhook rejects it before the object is stored. The controller rejects it if the object was stored some other way, and it writes no plan.

The network address (`10.20.30.0`) and the IPv4 broadcast (`10.20.30.255`) are not hosts. They are rejected too. `/31` and `/127` are the real exception used on point-to-point links: both addresses are usable, so that rule steps aside for those two sizes.

Value: bad data dies at the desk. It does not become a node that cannot route.

### a typo that claims the world

There is no technical limit, in the kernel, that stops a segment of `0.0.0.0/0`. There is a product limit. IPv4 segments must be `/8` through `/31`. IPv6 segments must be `/32` through `/127`. A cluster prefix is a slice of a network, not the network of every cluster you will ever have.

Value: one bad object cannot make every future object look like an overlap, and it cannot be applied as a default route by mistake.

### reinstall forgets the address

Metal3's job is to wipe and install. That is what you want for a dead disk. It is fatal for configuration that lived only on the disk.

The `Segment` survives in etcd on the management side of the API, not on the wiped disk. After the node boots, the helper's job is to put the address back because the object is still there. Phase 1 does not do this yet. The controller is shaped so the plan survives independently of any one boot.

Value: rebuilds stop being a secret network outage. This value becomes real in phase 2 and is only proven on a Sylva install in phase 5.

### the route someone added at 2am

Desired state is the plan. Actual state will be the helper's list of what netlink reports. Drift is `plan` minus `observed`, written into status. A human can see it. The next reconcile deletes what the plan does not contain.

Value: incident leftovers have a place they show up, and a loop that removes them. Also phase 2. Phase 1 does not pretend this already works.

### the map walks out the door

Git holds the YAML. `git log` is who changed the prefix. Flux's job is to make the cluster match that commit, including putting back a `Segment` that someone deleted by hand in the cluster.

Value: the plan survives vacations, laptops, and "I think we changed that in March." Phase 3.

### every site has a private copy

kcp publishes the `Segment` API once. Each site's workspace binds it. The same controller watches the export. Sites stop emailing spreadsheets.

Value: one rule set, many desks. Phase 4. kcp is not asked to boot servers to deliver this.

### changing an address takes forty minutes

If the address is cloud-init, the change is a reinstall. If the address is a `Segment`, the change is a new object (cidr and gateway are immutable, so it is delete plus create, on purpose) and a reconcile.

Value: the blast radius of a prefix edit is the helper, not the disk. Phase 5 is where this is visible, because only then is Sylva the thing that would otherwise have been re-run.

### planners have root

Humans get RBAC on `segments` in their namespace. The operator's service account gets `segments` and `segments/status` across namespaces, plus events. The helper, later, is the only process allowed to call netlink, and it accepts operations, not a shell.

Value: a bad pull request can still be bad, and it cannot also read every secret on the node. The operator stays unprivileged. Only the helper holds NET_ADMIN, and only inside its own network namespace in the lab.

## the object

```yaml
apiVersion: span.dev/v1alpha1
kind: Segment
metadata:
  name: lab
  namespace: campus
spec:
  cidr: 10.20.30.0/24
  gateway: 10.20.30.1
  cluster: lab
  vlan: 100        # optional
```

`cluster` is a DNS label. It names the workload cluster this prefix belongs to. Phase 1 stores it and includes it in the identity. It does not open a kubeconfig to that cluster yet.

`vlan` is a pointer in Go (`*int32`). Omitted means "no VLAN." The CRD allows 1 through 4094 only when the field is present.

Identity, written once, is the annotation `span.dev/identity`:

```text
10.20.30.0/24|10.20.30.1|lab
```

The cidr in that string is the canonical network address, so `10.20.30.00/24` cannot sneak in as a different promise. Changing cidr, gateway, or cluster after this annotation exists is reason `Immutable`. The old plan is left in status so phase 2 still knows what to remove. The user is told to delete the object and create another.

Finalizer: `span.dev/segment`.

Status:

- `observedGeneration` matches the spec generation the conditions describe
- `conditions` of type `Ready` and `Applied`
- `plan`, the operations, empty when the object must not be applied
- `connectedRoute`, the prefix the kernel will treat as on-link once the address exists

A plan for the example, VLAN included:

```yaml
plan:
  - action: add-vlan
    vlan: 100
  - action: add-address
    address: 10.20.30.1/24
connectedRoute: 10.20.30.0/24
```

There is no `add-route` via `10.20.30.1`. Once `10.20.30.1/24` is local, the kernel installs the connected route itself. A route whose nexthop is our own address would be a bug.

The parent interface for the VLAN is not in `v1alpha1`. Phase 2 adds it as a real schema change, which is the CRD-evolution exercise, instead of inventing a dummy device name now.

## walk through one reconcile

Code: `internal/controller/segment_controller.go`. Rules: `internal/planner`.

1. **Get** the object. Not found means it is gone. Stop.
2. **Deleting?** If our finalizer is set, unwind. Phase 1 unwind does nothing, because nothing was applied, then removes the finalizer. Return. The API server finishes the delete.
3. **Validate** the spec with the planner. On failure, set `Ready=False` / `Invalid`, clear the plan, do not add a finalizer, write status, return success.
4. **Stamp the promise.** If there is no finalizer or no identity annotation, set both from the current spec and update the object. Then keep going in the same call. An annotation update does not bump `generation`, so we must not wait for a new event.
5. **Immutable?** If the annotation exists and the spec's identity differs, set `Ready=False` / `Immutable`, keep the existing plan, return success.
6. **List** every segment from the live API reader. Find one that overlaps and beats us. A segment that is still deleting keeps the prefix until the helper has removed the address.
7. **Overlap loss.** Clear the plan, `Ready=False` / `Overlap`, requeue after 30 seconds in case the watch missed a delete.
8. **We win.** Build the plan. Ask the helper. `Ready=True` / `Planned`. `Applied=True` only when the helper reports the kernel. No helper means `Applied=False` / `PendingHelper`.
9. **Write status** only when it changed. `SetStatusCondition` keeps transition times stable so a quiet object stays quiet.

Events fire when the ready reason changes, not on every identical run.

The watch uses `GenerationChangedPredicate`. Spec edits and deletes wake the controller. Status writes do not. On top of that, any such event enqueues every segment, so the loser wakes up when the winner is deleted.

That fan-out is O(segments) reconciles per change, and each one lists all segments from the API server. For a campus of tens or hundreds of prefixes this is nothing. For tens of thousands it is the wrong shape. The fix, when the number is real, is an index, not a bigger sleep. The architecture should change before the data does.

## who may say no

Three layers, on purpose.

| layer | what it can refuse | what it cannot see |
| --- | --- | --- |
| CRD schema | missing fields, bad types, VLAN outside 1-4094, cluster name shape | "gateway is inside this cidr", overlap |
| validating webhook | spec rules, immutability, overlap against objects already stored | the create that is happening at the same millisecond |
| controller | all of the above, plus the race, plus objects admitted while the webhook was not installed | nothing that is stored, if the list works |

`failurePolicy: Fail` on the webhook means a dead webhook blocks segment creates and updates. Deletes are not in the webhook rules, so cleanup still works. That tradeoff is accepted: a few minutes where nobody can add a prefix, instead of a few minutes where bad prefixes get in.

Phase 1's in-cluster demo installs this webhook. `make run` on the host does not, because a kind API server cannot be assumed to reach a process on your laptop. The controller still guards status in that mode. `kubectl` will store a bad object, and the stamp will say `Invalid`. Do not confuse those two setups.

## technologies in this design

### kubernetes

Span uses the API server as the only database. There is no second store to get out of date. RBAC, audit, and `kubectl` come with that choice.

The cost: we inherit etcd's size limits and the API server's availability. If the API is down, no new promise is filed, and the helper (later) should keep the last applied config rather than wiping the host because it cannot phone home. Phase 2 has to fail closed on the host: no fresh instruction means "leave the last good programming," not "delete every address."

### custom resources

A CRD was chosen over a ConfigMap full of YAML because we need schema, a status subresource, admission, and watches. A ConfigMap would be an untyped bag, and overlap would be a program parsing hope.

Namespaced scope was chosen so RBAC can give a team create rights in one namespace. Cluster scope would make every segment editor a cluster-wide editor. The overlap list is still cluster-wide, which is why the operator's own Role is a ClusterRole.

`v1alpha1` is the storage version. There is one version so we do not pretend conversion works before we have a second schema worth converting. The VLAN parent device is the likely reason to grow a `v1`.

### the operator

controller-runtime is the usual Go controller framework: manager, cache, queue, health, metrics, webhook server. Writing a raw informer would teach internals and would also be a place to get retry and leader election wrong. The learning goal is the reconcile contract, not a private framework.

One replica, leader election off. Reconcile is idempotent, so a second replica would mostly fight on status resourceVersions. Turn election on before you run two replicas. The lease id is reserved in the flags as `span-operator.span.dev`.

### go

The module is `github.com/akrem/span`. Policy code in `internal/planner` has no client and no clock except the timestamps the caller passes in. That is why the overlap rules can be tested without envtest.

The controller is the only place that talks to the API. The webhook is the only place that turns field errors into an admission denial. Neither one reimplements parsing.

Errors from the API are wrapped and returned. User errors become conditions. That split is the whole error-handling policy. A reviewer should be suspicious of any new `return fmt.Errorf` whose cause is a bad spec.

### rust and netlink

Not in the repository yet. The plan's shape is the contract the helper will implement, so the Go side is not designed around a shell script.

The helper will be privileged and boring. It will not read Kubernetes. The operator will hand it operations and it will answer with what the kernel shows. That answer becomes status the operator writes. Two writers do not invent the plan.

A connected route is not an operation. Programming one would fight the kernel.

### flux

Not installed yet. The YAML under `config/` is already the git content Flux will apply: CRD, RBAC, deployment, webhook, sample. Phase 3 adds `GitRepository` and `Kustomization`, not a second copy of the manifests.

Sylva itself is a Flux project. Learning Flux here transfers to reading a Sylva deployment later: units are Helm releases and kustomizations, applied in dependency order, from a commit.

### argo cd

Not installed yet. It will deploy the same `config/` tree through an `Application`, on a cluster that Flux is not managing. The point of the exercise is to be fluent in both tools and to feel why two drift controllers on one Sylva management cluster is an outage.

### kcp

Not installed yet. Phase 4 adds an `APIResourceSchema` and an `APIExport` for `Segment`, a workspace that binds the export, and a controller built on multicluster-runtime's apiexport provider. The admission rules stay the same rules, pointed at that export.

The syncer, which copies pods onto a physical cluster, is a different kcp feature. Span does not need it to publish an API. Using it as a back-door provisioner would blur the line with Sylva.

### cluster api and metal3

Not installed yet. They enter with Sylva in phase 5, as upstream components Sylva already wires. CAPM3 selects `BareMetalHost` objects. Metal3's Ironic path inspects the machine, writes an image, and boots it. A virtual BMC (libvirt plus sushy-tools) is the public lab path when the hardware is one strong PC. A real BMC is the same workflow with real power control.

Span does not create `BareMetalHost` objects. If it did, this repo would be a second installer.

### sylva

Sylva is the public recipe: temporary kind bootstrap, Flux, Cluster API, a management cluster, pivot, then workload clusters. Enabling the `capm3` unit enables Metal3. We follow current upstream docs at the time we start phase 5, and we pin the versions we actually booted in the README. We do not vendor sylva-core.

After the workload cluster is up, Flux installs span onto it. A `Segment` for a prefix that the lab actually owns should land an address. Deleting the segment should remove it. That is the demo that proves the business story about reinstalls. Until that demo exists, the reinstall claim is a design, not a result.

### envtest, lint, metrics

envtest runs a real API server in the controller tests. The webhook tests do too, in their own process, with a local certificate the API server trusts. Planner tests do not need either.

`span_segment_reconcile_total{result=...}` counts outcomes: `ready`, `overlap`, `invalid`, `immutable`, `deleted`, `error`, `unchanged`. The label is the outcome, never the segment name. A name label would blow up Prometheus cardinality and would leak the address plan into a metrics system that often has a wider audience than the API.

The metric endpoint listens on `:8080` inside the pod and is not a public Service. `:8081` is health and readiness.

golangci-lint runs `gofmt`, `govet`, `staticcheck`, `errcheck`, `ineffassign`, `unused`, and `misspell`. The bar is "the tools we will actually keep on," not forty linters and a sea of exceptions.

## security

- The operator container runs as a non-root user, with a read-only root filesystem, and drops all capabilities. It does not need to program the kernel. The helper will, later, and it should be a different container with a different account.
- RBAC is a ClusterRole because overlap is cluster-wide: get, list, watch, update, patch on `segments`, update and patch on `segments/status` and finalizers, create and patch on events. No secret access. No node access. No wildcard verbs.
- Webhook certificates for the lab are a self-signed cert generated into `bin/certs`, which git ignores. The API server trusts it through `caBundle`. This is a lab trust root. A real install should use a cert manager and a private key that does not sit in a shell history longer than it must. The script writes only under `bin/`.
- The CRD does not store credentials. A prefix is not a password, and it is still a map of the campus. Treat git and the API audit log as sensitive operational data.
- Immutability of the identity is a safety control, not a style choice. A user who can edit spec must not be able to slide a promise onto a different prefix while a finalizer still thinks it knows what to delete.

## what happens under real traffic

"Traffic" here is API traffic and node churn, not HTTP through the prefix. Phase 1 has no data plane.

- A steady cluster with a few hundred segments and rare edits is a light load: one list and one status update per changed object, plus a wake-up of the others.
- A loop that updates an annotation on every segment will enqueue the full set each time. That is the fan-out. It stays correct and gets chatty.
- Status writes do not enqueue the world, because generation did not change. If a future patch removes that predicate, the operator will hot-loop as soon as it writes status. The predicate is load-bearing.
- Two rapid creates of the same prefix: both may be stored. One ends `Ready`, one `Overlap`. No plan is published for the loser. A human deletes the loser or picks a new prefix.
- API timeout during list: reconcile returns an error, the queue retries, no condition is published that says the object is healthy.
- Webhook down, failure policy Fail: creates and updates of segments fail. Existing plans stay. Deletes still proceed.
- Webhook down and someone has also turned `failurePolicy` to `Ignore`: the controller is the only guard, and `kubectl` will see success for a bad spec that then goes `Ready=False`. Do not do that in a cluster where humans treat a successful apply as proof.
- Node reinstall: the object remains, and the next reconcile asks the helper again. On this laptop the helper's network namespace is not a node, so a kind node reinstall does not prove bare metal.

## tradeoffs we actually made

- **Older object wins**, not "largest team" and not "first one the webhook happened to see." Time is visible on the object and needs no extra lock service. Clock ties fall back to the name. A user can still be surprised that their newer segment lost. The message names the winner.
- **Deleting objects keep the claim until unwind finishes.** The helper removes the address, then the finalizer goes. A new segment cannot take the prefix while the old address may still be on the link.
- **The older object keeps the prefix.** Both objects standing down would also be safe, and it would punish the legitimate owner whenever a duplicate appears.
- **Cidr, gateway, and cluster are immutable.** You cannot fix a typo in place. You delete and recreate. That is extra work for the user and the right work for a prefix that may already be on a host.
- **Link defaults to span0 and is not part of the promise.** The lab helper creates that dummy device in its own network namespace. A real node would name a different device. Changing the link does not require a new segment. The controller removes the address from the previous link first.
- **Fan-out listing.** Simple and correct at lab scale. It will not be the production shape at tens of thousands of segments.
- **Kind demo and Sylva demo are different claims.** Kind plus the helper self-test prove the API and one network namespace. Sylva would prove the reinstall story. This laptop cannot boot that lab.
- **Self-signed lab webhook.** Fast to run, wrong for a shared cluster. Replace it before anyone else depends on the endpoint.
- **Single replica.** Operationally simple. It is not highly available. A dead operator stops new plans and stops repair of drift. It does not, by itself, strip addresses, as long as the helper's failure mode is "keep last programming."
- **kcp overlap is per workspace.** The front-desk controller uses the client for the workspace that sent the event. It does not compare prefixes across workspaces. The cluster operator is what enforces the claim on the machines.
- **Flux and Argo CD stay on different clusters.** The argo lab uses kind cluster `span-argo` and checks that `span` has no argocd namespace.

## what is true today

- `Segment` v1alpha1, with v1 served only when `config/crd/conversion/segments.yaml` is installed. Storage stays v1alpha1. An empty link reads as `span0` through conversion.
- planner, controller, validating webhook, Rust helper, Go helper client
- tests for the planner, the controller, the helper client, conversion, and the webhook
- kind demo installs the helper and waits for Applied
- `hack/flux-lab.sh` and `hack/argo-lab.sh` install the two git tools on two clusters
- `hack/kcp-lab.sh` pins kcp v0.28.3 and runs the apiexport controller
- `hack/sylva-lab.sh` refuses. No workload cluster was provisioned here.

`Applied=True` is allowed only after the helper returns a report. A missing helper stays `PendingHelper`.
