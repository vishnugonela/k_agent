package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/pflag"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"

	"github.com/vishnugonela/k_agent/internal/agent"
	"github.com/vishnugonela/k_agent/internal/kubernetes"
)

const version = "0.1.0-dev"

type Reader interface {
	Get(context.Context, string, string, string) ([]kubernetes.Resource, error)
}

type Factory func(kubernetes.Options) (Reader, kubernetes.Target, error)

func newReader(options kubernetes.Options) (Reader, kubernetes.Target, error) {
	config, target, err := kubernetes.LoadConfig(options)
	if err != nil {
		return nil, target, err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, target, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return kubernetes.NewReader(client), target, nil
}

func Run(args []string, stdout, stderr io.Writer, factory Factory) int {
	flags := pflag.NewFlagSet("kagent", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	options := kubernetes.Options{}
	var output string
	var ollamaURL, ollamaModel, provider, openAIModel string
	var help bool
	flags.StringVar(&options.Kubeconfig, "kubeconfig", "", "path to kubeconfig")
	flags.StringVar(&options.Context, "context", "", "kubeconfig context")
	flags.StringVarP(&options.Namespace, "namespace", "n", "", "namespace override")
	flags.DurationVar(&options.Timeout, "request-timeout", 30*time.Second, "total request timeout (e.g. 30s)")
	flags.StringVarP(&output, "output", "o", "table", "output format: table or json")
	flags.StringVar(&ollamaURL, "ollama-url", "http://127.0.0.1:11434", "Ollama server URL for ask")
	flags.StringVar(&ollamaModel, "ollama-model", "llama3.1", "Ollama tool-capable model for ask")
	flags.StringVar(&provider, "provider", "ollama", "ask provider: ollama or openai")
	flags.StringVar(&openAIModel, "openai-model", "gpt-4.1-mini", "OpenAI tool-capable model for ask")
	flags.BoolVarP(&help, "help", "h", false, "show help")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kagent [flags] get RESOURCE [NAME]")
		fmt.Fprintln(stderr, "       kagent [flags] ask QUESTION")
		fmt.Fprintln(stderr, "       kagent version")
		fmt.Fprintln(stderr, "Resources: pods, deployments, statefulsets, daemonsets, jobs, services")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		flags.Usage()
		return 2
	}
	if help {
		flags.Usage()
		return 0
	}
	positional := flags.Args()
	if len(positional) == 1 && positional[0] == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	isGet := len(positional) >= 2 && len(positional) <= 3 && positional[0] == "get"
	isAsk := len(positional) == 2 && positional[0] == "ask"
	if !isGet && !isAsk {
		flags.Usage()
		return 2
	}
	if isGet {
		if err := kubernetes.ValidateKind(positional[1]); err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 2
		}
	}
	if output != "table" && output != "json" {
		fmt.Fprintln(stderr, "Error: --output must be table or json")
		return 2
	}
	if isAsk && output != "table" {
		fmt.Fprintln(stderr, "Error: ask only supports text output")
		return 2
	}
	if isAsk && provider != "ollama" && provider != "openai" {
		fmt.Fprintln(stderr, "Error: --provider must be ollama or openai")
		return 2
	}
	if isAsk && provider == "openai" && os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Fprintln(stderr, "Error: OPENAI_API_KEY is required for the OpenAI provider")
		return 1
	}
	if options.Timeout <= 0 {
		fmt.Fprintln(stderr, "Error: --request-timeout must be greater than zero")
		return 2
	}
	if factory == nil {
		factory = newReader
	}
	reader, target, err := factory(options)
	if err != nil {
		printError(stderr, err)
		return errorCode(err)
	}
	fmt.Fprintf(stderr, "Target: context=%s namespace=%s\n", target.Context, target.Namespace)
	ctx, cancel := context.WithTimeout(context.Background(), options.Timeout)
	defer cancel()
	if isAsk {
		var answer string
		var err error
		if provider == "openai" {
			fmt.Fprintf(stderr, "OpenAI: model=%s; question and requested summaries are sent to OpenAI; answers may be inaccurate\n", openAIModel)
			answer, err = (agent.OpenAIClient{APIKey: os.Getenv("OPENAI_API_KEY"), Model: openAIModel}).Ask(ctx, reader, target.Namespace, positional[1])
		} else {
			fmt.Fprintf(stderr, "Ollama: model=%s; question and requested summaries are sent to the configured server; answers may be inaccurate\n", ollamaModel)
			answer, err = (agent.Client{Endpoint: ollamaURL, Model: ollamaModel}).Ask(ctx, reader, target.Namespace, positional[1])
		}
		if err != nil {
			printError(stderr, err)
			return errorCode(err)
		}
		if _, err := fmt.Fprintln(stdout, answer); err != nil {
			fmt.Fprintf(stderr, "Error: write output: %v\n", err)
			return 1
		}
		return 0
	}
	name := ""
	if len(positional) == 3 {
		name = positional[2]
	}
	items, err := reader.Get(ctx, target.Namespace, positional[1], name)
	if err != nil {
		printError(stderr, err)
		return errorCode(err)
	}
	if output == "json" {
		err = json.NewEncoder(stdout).Encode(struct {
			kubernetes.Target
			Items []kubernetes.Resource `json:"items"`
		}{target, items})
	} else {
		writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(writer, "KIND\tNAMESPACE\tNAME\tSTATUS\tREADY\tRESTARTS")
		for _, item := range items {
			restarts := "-"
			if item.Restarts != nil {
				restarts = fmt.Sprint(*item.Restarts)
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", item.Kind, item.Namespace, item.Name, item.Status, item.Ready, restarts)
		}
		err = writer.Flush()
	}
	if err != nil {
		fmt.Fprintf(stderr, "Error: write output: %v\n", err)
		return 1
	}
	return 0
}

func printError(writer io.Writer, err error) {
	fmt.Fprintf(writer, "Error: %v\n", err)
	switch errorCode(err) {
	case 3:
		fmt.Fprintln(writer, "Check your credentials and ask an administrator for get/list access in the selected namespace.")
	case 4:
		fmt.Fprintln(writer, "Check the resource name, namespace, and whether the API is available on the cluster.")
	case 5:
		fmt.Fprintln(writer, "Check cluster connectivity and --request-timeout.")
	}
}

func errorCode(err error) int {
	if apierrors.IsUnauthorized(err) || apierrors.IsForbidden(err) {
		return 3
	}
	if apierrors.IsNotFound(err) {
		return 4
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || errors.As(err, &networkError) {
		return 5
	}
	return 1
}
