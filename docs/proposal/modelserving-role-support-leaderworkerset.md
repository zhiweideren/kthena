## ModelServing Role Support LeaderWorkerSet Proposal

### Summary

This proposal introduces LeaderWorkerSet (LWS) role support for Kthena ModelServing. The goal is to enable ModelServing to orchestrate leader/worker style “super-pod” workloads via LWS where it provides better lifecycle semantics (group-level rollout, coordinated restart behaviors, stable identity and networking patterns), while preserving the existing ModelServing user experience and integrations (ModelServer/ModelRoute selection and autoscaling).

The proposal evaluates three solution paths and recommends extending `ModelServing.spec.template.roles[]` to optionally be backed by LeaderWorkerSet resources, allowing mixed deployments where only specific roles use LWS.

### Motivation

ModelServing already models distributed inference patterns with ServingGroups and Roles, and Roles can include an entry pod and worker pods. However, many multi-host inference stacks and ecosystem tooling have standardized on LeaderWorkerSet as a unit-of-replication abstraction for leader/worker groups. Supporting LWS in ModelServing enables teams to reuse existing LWS-based workload definitions and operational practices while still integrating with Kthena’s serving routing, autoscaling, and scheduling policies.

#### Goals

- Support running a ModelServing Role using LeaderWorkerSet lifecycle semantics.
- Preserve existing integrations that select pods via labels (ModelRouter/ModelServer) and autoscaler behavior that targets entry pods.
- Support required Service behaviors (headless/service discovery) for leader/worker communication when LWS is used.
- Allow mixed roles: some roles backed by native ModelServing pod management, others backed by LWS.
- Keep the end-user API surface consistent and minimal, with clear compatibility rules.

#### Non-Goals

- Replacing ModelServing as the primary workload API with LeaderWorkerSet.
- Adding new scheduling capabilities beyond what ModelServing already supports (e.g., gang scheduling and topology policies are out of scope unless directly required for LWS integration).
- Building a generic “CRD translation framework” for arbitrary workload CRDs.
- Supporting clusters without the required LeaderWorkerSet CRDs/controller when LWS-backed roles are configured.

### Proposal

#### 1. Introduction

**Purpose**
- Add LeaderWorkerSet (LWS) role support in ModelServing to enable leader/worker group orchestration for multi-host inference and other tightly-coupled serving workloads.

**Scope and objectives**
- Define how ModelServing can reference, create, or interpret LWS resources for pod management.
- Ensure ModelServing, ModelServer/ModelRoute selection, and autoscaling continue to work without requiring users to redesign their operational model.
- Provide a migration path for existing ModelServing roles and for users bringing existing LWS specs.

**Expected benefits**
- Improved lifecycle control for multi-pod groups (group-level rollouts and restart coordination).
- Clear separation between workload replication unit (LWS group) and serving traffic integration (ModelServer/ModelRoute and Services).
- Easier adoption for users already standardized on LWS.

**Primary use cases**
- Multi-host inference where a leader coordinates sharded workers (e.g., distributed runtime, model-parallel serving).
- Workloads that need stable identity and discoverability per group and per pod within a group.
- Workloads where failure handling should treat a leader/worker set as a single recovery domain.

#### Definitions and terminology

- **ModelServing**: Kthena workload API for managing serving pods across one or more ServingGroups.
- **ServingGroup**: A unit of replication at the ModelServing level (ModelServing `spec.replicas`).
- **Role**: A sub-component within a ServingGroup, representing a serving function (e.g., prefill/decode). Roles can have an entry pod and worker pods.
- **Entry pod**: The role’s front/leader pod that typically exposes a metrics endpoint and/or receives requests from routing components. Kthena labels entry pods using `modelserving.volcano.sh/entry`.
- **LeaderWorkerSet (LWS)**: Kubernetes SIGs API for deploying a group of pods (leader + workers) as a unit of replication.
- **ModelServer/ModelRoute (ModelRouter)**: Networking/routing resources that associate traffic with pods via label selectors.
- **AutoScaler**: AutoscalingPolicyBinding-driven scaling for ModelServing and optionally a specific Role; metrics selection can target entry pods by label.

#### 2. Current Solution Analysis

Today, ModelServing reconciles a ModelServing object into ServingGroups and Roles and directly creates and manages Kubernetes Pods and, when needed, headless Services for leader/worker communication. Pods are labeled with well-known ModelServing labels (e.g., modelServing name, group name, role name, role id, and entry marker) to support higher-level integrations.

This proposal explores three approaches to supporting LeaderWorkerSet in ModelServing:

##### Solution 1: LWS Controller Integration

**Implementation**
- Users install and operate the upstream LWS controller and CRDs.
- Users deploy LeaderWorkerSet objects directly for pod management and lifecycle.
- Kthena components (ModelRouter/ModelServer/AutoScaler) integrate by selecting and interpreting LWS-managed pods via labels.

**Kthena adaptation**
- Kthena remains compatible by relying on labels that it already understands (e.g., `modelserving.volcano.sh/*`) or by extending its selection logic to accept LWS label schemas and map them to ModelServing concepts (ModelServing, ServingGroup, Role, entry/worker).
- ModelRouter/ModelServer: workload selectors match LWS-managed pods.
- AutoScaler: targets the leader/entry pods for metrics scraping by label.

**Limitations**
- Kthena does not provide native pod orchestration for these pods; lifecycle semantics are owned by the LWS controller.
- ModelServing does not become the single source of truth for the workload; users manage two parallel systems (LWS for pods, ModelServing-adjacent resources for serving integration).

##### Solution 2: Direct LWS CRD Support

**Implementation**
- Kthena Controller Manager accepts LeaderWorkerSet as an input CRD (as a first-class watched resource).
- Kthena natively performs pod management similarly to ModelServing, but the authoring and API are LWS.

**Features**
- Native pod management for leader/worker groups, including coordinated rollout/recovery policies consistent with Kthena conventions.
- Kthena can maintain a consistent internal model for ServingGroup/Role semantics while accepting LWS objects.

**Translation layer**
- A translation layer converts one or multiple LWS objects into an equivalent Kthena ModelServing internal representation (or materializes a shadow ModelServing object).
- This layer defines deterministic mappings for:
  - LWS replicas (groups) ↔ ServingGroup replicas
  - leader/worker templates ↔ Role entry/worker templates
  - service discovery and selectors ↔ Kthena Service conventions

##### Solution 3: Extended Role Configuration

**Implementation**
- Extend `ModelServing.spec.template.roles[]` to support an optional “role backend” mode.
- When a role is configured as LWS-backed, the ModelServing controller creates and manages LeaderWorkerSet resources instead of directly creating pods for that role.

**Behavior**
- The controller remains the primary reconciler of ModelServing.
- For each LWS-backed role instance, the controller creates/updates:
  - one LeaderWorkerSet per (ServingGroup, Role, RoleIndex) or one per (ServingGroup, Role) depending on the chosen mapping strategy
  - associated Services required by LWS (headless service for stable DNS; optional ClusterIP service for entry traffic if needed)
- For non-LWS roles, the controller uses the existing direct pod management behavior.

**Service integration**
- The controller ensures required Service behavior management continues to work:
  - headless discovery for intra-group communication
  - a stable selector for entry pods so autoscaling and routing continue to target the correct pods

#### 3. Comparative Analysis

##### Advantages

**Solution 1: LWS Controller Integration**
- Minimal changes to Kthena workload controller; leverages upstream LWS for lifecycle management.
- Fastest path to enable LWS users to run in the Kthena ecosystem.
- Clear responsibility split: LWS manages pods; Kthena manages serving integrations.
- Lower maintenance burden for Kthena regarding LWS lifecycle logic as LWS evolves.

**Solution 2: Direct LWS CRD Support**
- Single controller stack (Kthena) controls the end-to-end serving experience without requiring LWS controller deployment.
- Strong consistency with existing ModelServing behavior, observability, and status reporting.
- Enables Kthena-specific policy integration (gang/topology/rollouts) under a unified control plane.
- Avoids reliance on external controller versioning and cluster-level operational dependencies.

**Solution 3: Extended Role Configuration**
- Preserves ModelServing as the user-facing API while enabling LWS where it provides value.
- Supports mixed deployments: only roles needing leader/worker group semantics use LWS.
- Reuses existing ModelServing labels and selection patterns for routing and autoscaling.
- Clear migration path: existing ModelServing roles can opt into LWS without changing higher-level ModelServing constructs.

##### Disadvantages

**Solution 1: LWS Controller Integration**
- Two sources of truth for lifecycle: LWS controls pods, while Kthena controls serving integration objects.
- Limited ability for Kthena to enforce orchestration invariants (e.g., coordinated rollouts consistent with ModelServing status/conditions).
- Harder to provide a unified status view (ModelServing conditions) because workload health is indirectly derived.
- Increased user/operator complexity: installing and operating LWS controller and aligning label conventions.

**Solution 2: Direct LWS CRD Support**
- Highest implementation complexity: Kthena must track and replicate LWS semantics and edge cases.
- Higher long-term maintenance cost as upstream LWS API evolves.
- Potential user confusion between Kthena’s “LWS-compatible” behavior and upstream LWS behavior.
- Larger test surface area across multiple reconciler paths and upgrade compatibility scenarios.

**Solution 3: Extended Role Configuration**
- Requires upstream LWS CRDs/controller to be installed when LWS-backed roles are used (external dependency remains).
- Requires careful label/service compatibility design so ModelServer/ModelRoute/autoscaling continue to work consistently.
- Introduces dual reconciliation modes inside ModelServing controller (direct pods vs LWS resources).
- Mapping between ModelServing Role replication and LWS replica semantics must be clearly defined to avoid surprises.

#### 4. Recommendation

Recommend **Solution 3: Extended Role Configuration**.

**Rationale**
- Keeps ModelServing as the single user-facing API while enabling LWS-backed orchestration for roles that need it.
- Minimizes disruption to existing ModelServing users and preserves ModelServer/ModelRoute/autoscaling integrations that rely on ModelServing label conventions.
- Avoids the high complexity of re-implementing LWS semantics inside Kthena (Solution 2) while still enabling first-class ModelServing integration (unlike Solution 1).

**Implementation roadmap and timeline**
- Phase 0 (Week 1): Design and API review
  - Finalize CRD schema extension for roles (role backend mode and required fields).
  - Define label and Service compatibility contracts for LWS-backed roles.
- Phase 1 (Weeks 2–3): Controller implementation
  - Add reconciliation path that creates/updates LWS resources for configured roles.
  - Add cleanup and ownership/GC logic (OwnerReferences from LWS resources to ModelServing).
  - Ensure stable Service behavior (headless and/or entry selection).
- Phase 2 (Weeks 4–5): Integration and compatibility
  - Validate ModelServer selection and routing compatibility with LWS-managed pods via labels.
  - Validate autoscaling behavior that targets entry pods.
  - Add upgrade/migration behavior for switching a role between backends (guardrails and constraints).
- Phase 3 (Week 6): Documentation and examples
  - Add examples for common leader/worker inference stacks and mixed-role deployments.

**Resource requirements**
- 1 engineer familiar with Kthena ModelServing controller reconciliation logic.
- 1 reviewer with Kubernetes controller-runtime/lifecycle experience and familiarity with LWS concepts.
- Optional: 1 QA/infra engineer for end-to-end validation on a real cluster with LWS installed.

**Dependencies**
- LeaderWorkerSet CRDs installed in the cluster and the upstream LWS controller manager running when LWS-backed roles are configured.
- Agreement on label compatibility to keep ModelServer/ModelRoute and autoscaler selection stable.

### Design Details

This section specifies the recommended Solution 3 behavior and compatibility contracts.

#### Role backend mode (conceptual)

Extend `ModelServing.spec.template.roles[]` with a backend selector:

- **Native mode**: current behavior; ModelServing controller creates entry/worker pods directly.
- **LWS mode**: ModelServing controller creates LeaderWorkerSet resources and relies on the LWS controller to materialize pods.

The LWS mode must define:
- how ModelServing ServingGroup/Role/RoleIndex maps to LWS objects
- which labels must be present on the resulting pods for selection and status tracking
- which Services must be created and how selectors are defined

#### Mapping strategy (recommended default)

- **ModelServing `spec.replicas` (ServingGroups)**: remains the top-level replication unit for “instances”.
- **Role replicas within a ServingGroup**: each role replica maps to one LWS object representing one leader/worker group.

Mapping:
- LWS object name: `{servingGroupName}-{roleName}-{roleIndex}`
- LWS replicas: fixed to `1` (because each LWS object represents one role replica group)
- LWS group size: `1 + role.workerReplicas`
- LWS leader template: derived from role entry template
- LWS worker template: derived from role worker template (required in LWS mode)

This mapping keeps ModelServing’s existing “roleIndex” semantics aligned with LWS’ unit-of-replication (one group per role replica) and avoids adding another replication dimension inside a single LWS object.

#### Label and selector compatibility

To keep existing integrations stable, all pods created by LWS for a ModelServing-managed role must carry the same core labels ModelServing uses today:

- `modelserving.volcano.sh/name`: ModelServing name
- `modelserving.volcano.sh/group-name`: ServingGroup name
- `modelserving.volcano.sh/role`: role name
- `modelserving.volcano.sh/role-id`: role id (derived from role name + role index)
- `modelserving.volcano.sh/entry`: `"true"` for leader/entry pod; absent or `"false"` for workers

These labels are defined in Kthena workload API constants and are relied upon by other controllers for selection and behavior.

#### Service behavior

In LWS mode, the controller manages:

- **Headless service** for stable DNS within the leader/worker group, keyed by `(ServingGroup, Role, RoleIndex)` and selecting pods of that group.
- **Optional entry service** (ClusterIP) if needed to provide a stable endpoint for other components; selection must target only entry pods.

Service selectors must include the role-id and entry marker where applicable to avoid selecting worker pods when only entry pods should be routed or scraped.

#### Autoscaling behavior

Autoscaling integrations should continue to prefer entry pods:
- For ModelServing/Role target, the autoscaler already assumes an entry label exists and can be used to filter metrics endpoints.
- In LWS mode, the leader pod is treated as the entry pod and must expose the metrics endpoint configured by users.

#### Status and readiness

ModelServing controller should compute readiness for LWS-backed roles by:
- listing pods by the standard ModelServing selectors and counting ready entry/worker pods per role replica, or
- reading LWS status (updated/ready replicas) and projecting into ModelServing role readiness, with a clear definition of “ready role replica”.

The initial implementation should prefer pod-based readiness to minimize coupling to LWS API status fields, while still requiring the LWS controller to exist.

#### Example (conceptual) ModelServing with an LWS-backed role

```yaml
apiVersion: workload.serving.volcano.sh/v1alpha1
kind: ModelServing
metadata:
  name: llama-multihost
  namespace: default
spec:
  replicas: 1
  schedulerName: volcano
  template:
    roles:
      - name: prefill
        replicas: 1
        workerReplicas: 7
        entryTemplate:
          spec:
            containers:
              - name: leader
                image: your-image:tag
                ports:
                  - containerPort: 8080
        workerTemplate:
          spec:
            containers:
              - name: worker
                image: your-image:tag
        lws:
          enabled: true
          rolloutStrategy: RollingUpdate
```

### Test Plan

- Unit tests for reconciliation logic:
  - LWS-backed role creates expected LWS objects and Services.
  - Label contracts are applied to leader and worker pods via templates.
  - Switching role backend modes is rejected or supported with deterministic migration rules.
- Integration tests with envtest or a real cluster:
  - LWS controller installed; verify pods materialize and are labeled correctly.
  - ModelServer/ModelRoute selection picks up entry pods for routing.
  - AutoscalingPolicyBinding targeting ModelServing/Role selects only entry pods for metrics.
- End-to-end validation:
  - Multi-host inference sample that requires leader/worker coordination and service discovery.

### Alternatives

- **Adopt Solution 1** for faster enablement where Kthena only consumes LWS pods via label conventions, at the cost of losing ModelServing-level workload ownership.
- **Adopt Solution 2** if cluster environments cannot or should not install upstream LWS, at the cost of significant re-implementation effort.
- **Do nothing** and continue using native ModelServing entry/worker pods; acceptable when LWS ecosystem compatibility is not required.

### Appendices

#### A. Conceptual diagrams

**A.1 Native ModelServing role (today)**

```text
ModelServing
  └─ ServingGroup (replicas=N)
      └─ Role (replicas=R)
          ├─ Entry Pod (index=0)  [modelserving.volcano.sh/entry=true]
          └─ Worker Pods (index=1..M)
```

**A.2 ModelServing role backed by LWS (proposed)**

```text
ModelServing
  └─ ServingGroup (replicas=N)
      └─ Role (replicas=R)
          └─ LeaderWorkerSet (one per role replica)
              ├─ Leader Pod        [modelserving.volcano.sh/entry=true]
              ├─ Worker Pods       [modelserving.volcano.sh/entry=false]
              └─ Headless Service  (stable DNS for group membership)
```

#### B. Glossary (technical terms)

- **Gang scheduling**: scheduling multiple pods together as an all-or-nothing unit.
- **Headless service**: Kubernetes Service with no cluster IP, used for stable DNS and direct pod discovery.
- **Label selector**: a Kubernetes label-based query used to select target pods for routing, services, and metrics scraping.

#### C. References and prior art

- LeaderWorkerSet (upstream): https://github.com/kubernetes-sigs/lws
- Kthena ModelServing controller architecture: docs/kthena/docs/architecture/model-serving-controller.mdx
- Kthena workload label keys: pkg/apis/workload/v1alpha1/labels.go
