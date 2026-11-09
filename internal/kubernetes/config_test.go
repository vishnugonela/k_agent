package kubernetes

import (
	"path/filepath"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestLoadConfigSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	config := clientcmdapi.Config{
		CurrentContext: "first",
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {Server: "https://example.invalid"},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"first":  {Cluster: "cluster", Namespace: "first-ns"},
			"second": {Cluster: "cluster", Namespace: "second-ns"},
			"empty":  {Cluster: "cluster"},
		},
	}
	if err := clientcmd.WriteToFile(config, path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	tests := []struct {
		name    string
		options Options
		want    Target
	}{
		{"current context", Options{}, Target{"first", "first-ns"}},
		{"context override", Options{Context: "second"}, Target{"second", "second-ns"}},
		{"namespace override", Options{Context: "second", Namespace: "override"}, Target{"second", "override"}},
		{"default namespace", Options{Context: "empty"}, Target{"empty", "default"}},
		{"explicit kubeconfig", Options{Kubeconfig: path}, Target{"first", "first-ns"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.options.Timeout = 5 * time.Second
			clientConfig, target, err := LoadConfig(test.options)
			if err != nil {
				t.Fatal(err)
			}
			if target != test.want || clientConfig.Timeout != test.options.Timeout {
				t.Fatalf("got target %+v and timeout %v, want %+v and %v", target, clientConfig.Timeout, test.want, test.options.Timeout)
			}
		})
	}
	if _, _, err := LoadConfig(Options{Context: "missing"}); err == nil {
		t.Fatal("missing context should fail")
	}
	if _, _, err := LoadConfig(Options{Kubeconfig: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing explicit kubeconfig should fail")
	}
}
