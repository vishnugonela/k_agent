# Kagent development guide

This guide describes a proposed implementation for the goals in [README.md](README.md) and the phases in [RoadMap.md](RoadMap.md). A read-only CLI MVP is available; see the README for its current scope. Commands and interfaces labeled **proposed** are targets, not necessarily existing features.

## 1. Goals and scope

Build a Kubernetes assistant that can:

- Inspect workloads, pod health, events, logs, and available resource metrics from a CLI.
- Explain Kubernetes API resources and diagnose common workload failures using observable evidence.
- Propose and, with explicit approval, apply manifest changes.
- Integrate with IDEs, packaging and build tools, CI/CD systems, monitoring, and dashboards.

Start with a single-cluster, read-only CLI. Do not start with autonomous remediation, plugins, or a dashboard. Human approval and Kubernetes authorization remain mandatory for the first write-capable release.

### Initial decisions

| Topic | Proposed default | Reason |
| --- | --- | --- |
| Core language | Go | Kubernetes ecosystem support and portable CLI binaries. |
| Kubernetes access | Official `client-go` and API machinery | Typed clients, discovery, authentication, and API error handling. |
| Cluster selection | Current kubeconfig context; explicit overrides | Familiar behavior and visible targeting. |
| LLM integration | Provider-neutral adapter; provider selected later | Avoid coupling Kubernetes logic to one vendor. |
| IDE protocol | Model Context Protocol (MCP) | Reuse core tools across compatible IDE hosts. |
| Deployment changes | Server-side dry run, diff, and explicit approval | Review before changing a cluster. |

Confirm the module path, supported Kubernetes versions, model provider, and data-handling policy before implementing provider integration. Check for naming conflicts with existing projects before publishing under `kagent`.

## 2. Development environment

Install Go, Git, `kubectl`, a local Kubernetes distribution such as kind, and Docker or another container runtime supported by that distribution. Add Helm for the packaging phase. Follow each tool's official installation instructions; record tested versions when CI is introduced.

Create a disposable test cluster, not a production cluster:

```sh
kind create cluster --name kagent-dev
kubectl config current-context
kubectl get nodes
```

When finished, remove it with `kind delete cluster --name kagent-dev`. Always inspect the current context before running commands that change resources. For metric tests, install a Metrics Server compatible with the local cluster and verify its readiness; it is not automatically available in every cluster.

The Go module and pinned dependencies are already initialized in `go.mod` and `go.sum`. Choose `client-go` versions compatible with the Kubernetes versions the project tests; define and continuously test the supported version range rather than assuming every cluster version works.

## 3. Proposed architecture

```text
CLI / MCP / future dashboard
             |
   Application services and policy
       /         |         \
Kubernetes   Agent loop    Manifest service
  adapter    / LLM adapter   and validation
       \         |         /
       Configuration, audit, telemetry
```

Keep the Kubernetes and manifest services usable without an LLM. Frontends call the same application services; they must not implement independent authorization or approval logic.

### Suggested repository layout

```text
cmd/kagent/                 CLI entry point
internal/cli/               commands, flags, output formatting
internal/config/            configuration and kubeconfig selection
internal/kubernetes/        clients, discovery, workload operations
internal/metrics/           metrics adapters
internal/agent/             tool orchestration and response assembly
internal/llm/               provider interface and implementations
internal/manifest/          parsing, validation, diffs, and apply
internal/policy/            authorization, approval, and restrictions
internal/audit/             structured audit events and redaction
internal/mcp/               MCP transport and tool definitions
internal/plugins/           future plugin contracts and lifecycle
api/                        versioned public contracts, if needed
test/integration/           local-cluster integration tests
test/fixtures/              sanitized manifests and failure examples
docs/                       user and contributor documentation
```

This is a target layout, not a description of every existing package. Configuration currently lives in `internal/kubernetes`. Introduce packages only when their features are implemented. Keep interfaces small and define them at the consumer boundary so tests can substitute adapters.

### Request flow

1. Parse the command and determine the target context and namespace.
2. Load configuration, establish a bounded request context, and construct authenticated Kubernetes clients.
3. For natural-language requests, let the model propose calls only from an allowlisted tool registry; validate each call against its schema and policy.
4. Execute authorized reads and return bounded, sanitized results with resource identifiers and timestamps.
5. Produce an answer that separates observed facts, hypotheses, and recommended next steps. State when evidence is unavailable.
6. For changes, prepare a plan and diff, request approval, revalidate the target and preconditions, then apply and record the result.

The model is never an authorization authority. Kubernetes RBAC is enforced by the API server; application policy and approval are additional safeguards, not replacements.

## 4. Phase 1: read-only Kubernetes intelligence

### Implementation sequence

1. **Project foundation:** initialize the Go module; add formatting, linting, unit tests, CI, and a version command. Define configuration and typed errors.
2. **Kubernetes connection:** use kubeconfig loading rules, honoring explicit kubeconfig/context flags and `KUBECONFIG`. Display the effective context and namespace; never silently switch clusters. Use timeouts and cancellation.
3. **Discovery and workloads:** implement paginated lists and named-resource reads for pods, deployments, statefulsets, daemonsets, jobs, and services. Use discovery for supported API resources and handle unknown kinds and forbidden responses.
4. **Inspection:** expose status, conditions, container restart counts, recent events, and bounded logs. Handle missing containers, terminated pods, and RBAC failures individually. Avoid unrestricted log streaming by default.
5. **Metrics:** read pod CPU and memory from the metrics API when available. Node or pod disk usage requires an explicitly configured provider, such as a suitable monitoring backend; do not claim Metrics Server supplies disk usage. Explain missing, stale, or unsupported metrics.
6. **API documentation:** use cluster API discovery/OpenAPI for resource and field information where available; document Kubernetes version and gaps. Do not assume a cluster schema contains a complete human-readable reference.
7. **Natural-language interface:** add a provider interface and read-only tool registry. Limit tool calls, output size, execution time, and conversation context; return source resource references and explain uncertainty.

### Proposed CLI contract

The following examples describe the intended user experience. `get` and read-only `ask` are implemented; `describe` remains proposed. The current agent only has workload summaries, so it cannot reliably diagnose why a pod is not ready:

```sh
kagent --context kind-kagent-dev --namespace default get pods
kagent --context kind-kagent-dev describe deployment web
kagent --context kind-kagent-dev ask "Why is pod web-123 not ready?"
```

Add machine-readable output, such as JSON, alongside human-readable output. Define stable exit codes for usage errors, authentication/authorization failures, missing resources, and unavailable services. Do not expose secrets in outputs or diagnostics.

### Phase 1 acceptance criteria

- A user can inspect supported workloads in a selected namespace without granting write permissions.
- Answers cite the resource data or events used and identify absent evidence.
- Missing metrics, denied permissions, unavailable clusters, and request timeouts produce actionable errors.
- The CLI makes no modifying Kubernetes API requests in read-only mode; tests verify this.
- Unit tests and local-cluster integration tests pass in CI, including a restricted-RBAC account.

## 5. Phase 2: troubleshooting and safe changes

### Diagnosis

Implement deterministic evidence collectors before adding LLM explanations:

- **CrashLoopBackOff:** collect waiting reasons, restart counts, previous container termination details, relevant events, and bounded previous logs when available.
- **ImagePullBackOff:** inspect image name, waiting messages, and pull-related events; recommend checking registry access and image references without reading secret values.
- **Pending:** inspect pod conditions and scheduling events; distinguish unschedulable resource requests, scheduling constraints, and unbound volumes when evidence supports them.

Return ranked hypotheses only when supported by evidence; scheduling and runtime messages may be incomplete or misleading. Tests should cover missing logs, multiple containers, unavailable events, and misleading or absent signals.

### Manifest workflow

1. Parse YAML or JSON documents and preserve multi-document inputs. Reject malformed or unexpectedly large inputs.
2. Resolve resource kinds and namespaces through discovery; validate schema where available.
3. Generate an explicit change plan and human-readable diff. Use server-side dry run against the selected cluster; explain that dry-run success does not guarantee a later apply will succeed.
4. Present context, namespace, resource identity, operation, and diff for approval. Approval must bind to the exact plan and target; regenerate approval if either changes.
5. Recheck permissions and resource preconditions immediately before execution. Apply with Kubernetes concurrency/conflict handling; never silently force conflicts.
6. Report per-resource results and audit success or failure. Provide recovery or rollback guidance, while explaining that multi-resource operations are not atomic and rollback is not always possible.

Delete operations need a separate explicit confirmation and a clear resource identity. Disallow unattended writes by default. Keep a deterministic, non-LLM command path for the same validation and approval controls.

**Acceptance:** Tests prove that rejected or changed plans cannot be applied, that dry-run failures block execution, and that conflicting changes are surfaced rather than overwritten.

## 6. Phase 3: IDE integration

- Implement an MCP server over the shared application services, with separate read-only and write-capable tools and schema-validated inputs.
- Support workspace-aware manifest inspection and produce reviewable file edits; restrict filesystem access to approved workspace paths and reject path traversal.
- Integrate with VS Code through an appropriate extension or MCP support, depending on host capabilities. `/kagent` is a target interaction, not guaranteed to be available in every IDE.
- Obtain explicit approval before editing files, applying cluster changes, or creating commits or pull requests. Surface the same target and policy controls as the CLI.
- Test MCP tool contracts, workspace boundaries, cancellation, and host compatibility.

**Acceptance:** An IDE user can diagnose a workload and review a manifest diff without giving the model unrestricted filesystem or cluster access.

## 7. Phase 4: packaging and delivery

- Generate Helm charts with configurable values and validate them with `helm lint` and rendered-manifest checks. Reuse Helm instead of implementing a new package manager initially.
- Provide opt-in templates for application architecture, Dockerfiles, and CI/CD. Include language-specific probes, resource requests, configuration, and security considerations; treat generated templates as reviewable starting points.
- Integrate a selected container engine through an adapter. Image builds execute project-controlled build instructions: require explicit approval, isolate the build environment, and avoid passing credentials to untrusted builds.
- Add image-scanning integration and report scanner availability and results without claiming a clean scan guarantees safety.
- Start with GitHub Actions templates; add Jenkins through a separate adapter. Use least-privilege credentials and never generate workflows containing hard-coded secrets.
- Track rollout status and provide rollback guidance or approved rollback actions. Do not assume every workload supports identical rollback behavior.

**Acceptance:** A sample application can be rendered, validated, built, and deployed to the disposable cluster with review at each state-changing step.

## 8. Phase 5: plugins and observability

- Define a versioned plugin manifest, capability list, configuration schema, lifecycle, error contract, and compatibility policy.
- Prefer isolated processes with explicit capabilities over loading arbitrary third-party code into the agent process. Isolation alone is not a security sandbox; apply OS-level controls where needed.
- Add integrations incrementally: Prometheus for configured metrics, Grafana for dashboard links or APIs, and Argo CD for GitOps status. Clearly distinguish direct cluster changes from GitOps-managed changes.
- Support YAML/JSON generation and linting through reusable services. Add shell completion first; make predictive suggestions optional and never execute them automatically.
- Define how users install, authenticate, disable, and audit plugins. Treat external MCP servers as untrusted integrations and apply the same approval and data boundaries.

**Acceptance:** A plugin can be installed and disabled independently, exposes only declared capabilities, and cannot bypass central policy and approval checks.

## 9. Phase 6: enterprise platform

- Add a dashboard backed by the same APIs and policies as the CLI; authenticate users and authorize access per cluster and operation.
- Support multiple named clusters with explicit per-request targeting and credential isolation.
- Integrate OIDC where appropriate, define service and user identities, and map permissions to Kubernetes RBAC and application policy.
- Add structured audit retention, operational metrics, tracing, and administrative policy configuration. Establish retention and access controls before centralizing sensitive data.
- Consider autonomous remediation only after defining eligible actions, scope, policy gates, rate limits, incident escalation, and rollback procedures. Keep it disabled by default.

**Acceptance:** Cross-cluster access controls, audit trails, and disabled-by-default automation are verified in security and integration tests.

## 10. Security and data handling

- Use least-privilege Kubernetes identities. Never request cluster-admin access as a default installation step.
- Treat Kubernetes objects, logs, repository files, plugin outputs, and tool responses as untrusted data: prompt injection must not change tool permissions, target selection, or approval requirements.
- Avoid retrieving Secret values. Redact sensitive fields before display, logging, audit, or model submission, while recognizing that arbitrary logs may contain secrets that automated redaction misses.
- Make external model data transfer explicit and configurable; document the provider, data sent, retention policy, and opt-out or local-provider options. Do not send credentials or kubeconfig files to a model.
- Protect kubeconfig credentials and audit records; do not persist raw prompts, logs, or tool outputs by default.
- Set size limits, timeouts, rate limits, and tool-call budgets. Validate all tool arguments and never execute arbitrary model-generated shell commands.
- Pin dependencies, scan for vulnerabilities, and review updates. Document supported versions and publish a security reporting process before release.

## 11. Test and release strategy

| Test level | Coverage |
| --- | --- |
| Unit | Configuration, parsing, redaction, policy, tool schemas, diagnosis, and output formatting. |
| Adapter | Kubernetes fake clients and recorded/synthetic provider responses; include permission and timeout failures. |
| Integration | Real disposable cluster, discovery, RBAC, dry runs, events, metrics availability, and conflict handling. |
| End-to-end | CLI and MCP journeys, approval flows, and deployment of sample applications. |
| Security | Prompt injection, secret leakage, path traversal, unauthorized writes, and plugin isolation boundaries. |

Fake clients do not faithfully implement all API-server behavior, admission, or RBAC; cover these with real-cluster tests. Avoid sending real cluster data to external LLMs in CI. Use deterministic mock providers for gating tests; evaluate model quality separately with sanitized fixtures and explicit metrics.

Once the module exists, a baseline local check is:

```sh
gofmt -w .
go test ./...
go vet ./...
```

`gofmt -w` modifies files. CI should check formatting without silently rewriting the checkout, then run tests, static analysis, dependency checks, and integration tests. Publish binaries only after defining versioning, supported platforms, artifact provenance, and a release checklist.

## 12. First implementation backlog

1. Confirm the module path, language, supported Kubernetes versions, and model/data policy.
2. Initialize the Go project, CLI skeleton, CI, and contributor instructions.
3. Implement kubeconfig and explicit context/namespace selection with unit tests.
4. Add read-only workload listing and inspection with stable output and error handling.
5. Add events, bounded logs, and optional CPU/memory metrics with integration tests.
6. Extend the existing Ollama and opt-in OpenAI adapters and read-only tool contracts with additional approved evidence sources.
7. Add end-to-end tests for natural-language answers and restricted RBAC.
8. Review Phase 1 acceptance criteria before starting any write-capable feature.
