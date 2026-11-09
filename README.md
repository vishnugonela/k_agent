# Agent for Kubernetes cluster management

See the [development guide](DEVELOPMENT.md) for the proposed architecture, implementation phases, and testing strategy.

## Read-only CLI MVP

Requirements: Go 1.23 or newer and, for cluster access, a kubeconfig with permission to `get` and `list` the requested resources. The `get` command requires no model or external AI service. The Go module path follows the repository origin; no release or publication is implied.

```sh
go build -o kagent ./cmd/kagent
./kagent version
./kagent --kubeconfig ~/.kube/config --context my-context get pods
./kagent get deployment web --namespace my-namespace
./kagent get services -o json --request-timeout 10s
```

Use `./kagent --help` for all flags. `get RESOURCE` lists resources, while `get RESOURCE NAME` retrieves one. Supported resources are pods, deployments, statefulsets, daemonsets, jobs, and services; singular names also work. Lists are paginated. The current kubeconfig context and its namespace are used by default, including `KUBECONFIG` loading rules; `--kubeconfig`, `--context`, and `--namespace` override them. The effective context and namespace are printed to stderr, leaving JSON on stdout. The default request timeout is 30 seconds; it bounds the overall resource retrieval after client setup.

Table and JSON output contain only resource names, namespaces, kinds, and selected status, readiness, and restart information. JSON is a summary, not the complete Kubernetes object. Secret resources, environment variables, annotations, and other arbitrary object fields are not displayed. Resource names and status values themselves may still contain sensitive information. The API server and the authenticated identity remain responsible for authorization; no write operations are implemented.

Exit codes: `0` success, `1` configuration or other error, `2` usage error, `3` authentication or authorization error, `4` resource or API not found, and `5` timeout or network error. Kubeconfig authentication mechanisms supported by client-go, including exec plugins, may require locally installed credential helpers.

Not implemented: `describe`, discovery, events, logs, metrics, changes to resources, or the later integrations described below. Kubernetes version compatibility has not yet been validated against real clusters. Unit and local HTTP fixture tests do not replace real-cluster or restricted-RBAC integration tests.

## AI agent mode

`ask` uses a model to select read-only workload queries in the selected namespace and answer using their summaries. It cannot change resources or inspect secrets, logs, events, or metrics. Responses are model-generated and can be incorrect or omit evidence; verify them against Kubernetes. A tool call returns at most 100 summaries to the model, and large results may be rejected. The underlying paginated read still retrieves the full list.

### Ollama setup

Install and start [Ollama](https://ollama.com/download), then pull a model that supports tool calling:

```sh
ollama pull llama3.1
```

Build the CLI as shown above and ask a question:

```sh
./kagent --context my-context --namespace my-namespace --request-timeout 2m ask "Which pods are not ready?"
```

The default server is `http://127.0.0.1:11434` and the default model is `llama3.1`. Use `--ollama-url` and `--ollama-model` to change them. Model downloads and inference require sufficient local disk and memory. If the server is unreachable, ensure it is running; if a model cannot use tools, choose a tool-capable model. The timeout covers both model requests and Kubernetes reads after client setup.

### OpenAI setup

1. Create an OpenAI API key and enable API access and billing as appropriate for your account. Choose a model available to your account that supports function calling.
2. Set `OPENAI_API_KEY` in your shell using your secret manager or another secure method. Do not commit the key or paste it into a command stored in shell history.
3. Explicitly select OpenAI and, if needed, a different model.

```sh
./kagent --provider openai --openai-model gpt-4.1-mini --context my-context --namespace my-namespace --request-timeout 2m ask "Which pods are not ready?"
```

OpenAI is never selected by default. It receives the question and any workload summaries requested by the model, including resource names, namespace, status, readiness, and restart counts; these can be sensitive. The client sets `store: false` on Responses API requests, but this does not establish zero retention: review [OpenAI's data controls](https://platform.openai.com/docs/guides/your-data) and your organization's policy before use. API usage may incur charges. The Ollama server receives the same data; an explicitly configured remote server may send it outside your machine. Neither provider receives kubeconfig or Secret objects from these tools, but user questions may themselves contain sensitive data.

Development checks:

```sh
gofmt -w cmd internal
go test ./...
go vet ./...
```

## Planned capabilities

The following original goals are future plans, not descriptions of the current MVP.

### Enable kagent as CLI interface - Gen AI

- Agent suppose to be track all pod info
- Able to give any workload info (deployment, statefulset etc)
- Agent can manage documentation of entire kubernetes api
- Can able to deploy,update,upgrade and delete any workloads
- Enable pod resources status (cpu,memeory,disk)

### Enable kagent as an agentic mode (/kagent)

- kagent is capable to run/maintain in any IDE , like /kagent
- This agent can cable to edit manifest files(yamls) for code fix on error handling.
- Agent is capable to do packaging (similar to helm charts)
- Enable architecture design for any app deployment based on its platform (java,python etc)
- Agent can track cluster monitoring too.
- Enable agent for error handling on pod lifecycles (PodCrashloopbackup, imagepullerrrors, pending state , etc)

### Enable kagent as integration to third-party tools/software - plugin

- Agent can call/operate/include any 3rd party tools with plugin options to cluster.
- Capable to call any container engine to build images, provision containers/pods.
- Help to build CI/CD workflow or enable to integrate with github actions/any.
- Enable UI dashboard for cluster using agent ( kubernetes dashboard or any other UI dashboards)
- Include yaml/json generation and liniting features in kagent.
- Also, enable auto-fill the command options/arguments with predictive analysis.
