package kubernetes

import (
	"fmt"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Options struct {
	Kubeconfig string
	Context    string
	Namespace  string
	Timeout    time.Duration
}

type Target struct {
	Context   string `json:"context"`
	Namespace string `json:"namespace"`
}

func LoadConfig(options Options) (*rest.Config, Target, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if options.Kubeconfig != "" {
		rules.ExplicitPath = options.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: options.Context}
	if options.Namespace != "" {
		overrides.Context.Namespace = options.Namespace
	}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	raw, err := loader.RawConfig()
	if err != nil {
		return nil, Target{}, fmt.Errorf("load kubeconfig: %w", err)
	}
	contextName := options.Context
	if contextName == "" {
		contextName = raw.CurrentContext
	}
	if contextName == "" {
		return nil, Target{}, fmt.Errorf("no current kubeconfig context; select one with --context")
	}
	if _, ok := raw.Contexts[contextName]; !ok {
		return nil, Target{}, fmt.Errorf("kubeconfig context %q not found", contextName)
	}
	namespace, _, err := loader.Namespace()
	if err != nil {
		return nil, Target{}, fmt.Errorf("resolve namespace: %w", err)
	}
	config, err := loader.ClientConfig()
	if err != nil {
		return nil, Target{}, fmt.Errorf("configure context %q: %w", contextName, err)
	}
	config.Timeout = options.Timeout
	return config, Target{Context: contextName, Namespace: namespace}, nil
}
