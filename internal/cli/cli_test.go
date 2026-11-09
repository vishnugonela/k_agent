package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/vishnugonela/k_agent/internal/kubernetes"
)

type fakeReader struct {
	err error
}

func (reader fakeReader) Get(ctx context.Context, namespace, kind, name string) ([]kubernetes.Resource, error) {
	if reader.err != nil {
		return nil, reader.err
	}
	return []kubernetes.Resource{{Kind: "Pod", Namespace: namespace, Name: name, Status: "Running"}}, nil
}

func TestCLIOutputAndFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	factory := func(options kubernetes.Options) (Reader, kubernetes.Target, error) {
		if options.Context != "chosen" || options.Namespace != "team" || options.Timeout != 2*time.Second || options.Kubeconfig != "config" {
			t.Fatalf("unexpected options: %+v", options)
		}
		return fakeReader{}, kubernetes.Target{Context: options.Context, Namespace: options.Namespace}, nil
	}
	code := Run([]string{"get", "pods", "web", "--context", "chosen", "-n", "team", "--kubeconfig", "config", "--request-timeout", "2s", "-o", "json"}, &stdout, &stderr, factory)
	if code != 0 || !strings.Contains(stderr.String(), "context=chosen namespace=team") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output struct {
		Context   string                `json:"context"`
		Namespace string                `json:"namespace"`
		Items     []kubernetes.Resource `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil || output.Context != "chosen" || output.Namespace != "team" || len(output.Items) != 1 || output.Items[0].Name != "web" {
		t.Fatalf("output=%s error=%v", stdout.String(), err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"--context", "chosen", "--namespace", "team", "--kubeconfig", "config", "--request-timeout", "2s", "get", "pod", "web"}, &stdout, &stderr, factory)
	if code != 0 || !strings.Contains(stdout.String(), "NAME") || !strings.Contains(stdout.String(), "web") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestAskWithOllama(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if requests == 1 {
			fmt.Fprint(writer, `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"get_workloads","arguments":{"resource":"pods","name":"web"}}}]}}`)
			return
		}
		fmt.Fprint(writer, `{"message":{"role":"assistant","content":"Pod team/web is Running."}}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ask", "Is web running?", "--ollama-url", server.URL, "--ollama-model", "test"}, &stdout, &stderr, func(kubernetes.Options) (Reader, kubernetes.Target, error) {
		return fakeReader{}, kubernetes.Target{Context: "chosen", Namespace: "team"}, nil
	})
	if code != 0 || !strings.Contains(stdout.String(), "Pod team/web is Running.") || !strings.Contains(stderr.String(), "context=chosen namespace=team") || requests != 2 {
		t.Fatalf("code=%d stdout=%s stderr=%s requests=%d", code, stdout.String(), stderr.String(), requests)
	}
}

func TestAskProviderValidation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	for _, test := range []struct {
		args []string
		code int
	}{
		{[]string{"ask", "test", "--provider", "unknown"}, 2},
		{[]string{"ask", "test", "--provider", "openai"}, 1},
		{[]string{"ask", "test", "-o", "json"}, 2},
	} {
		code := Run(test.args, &bytes.Buffer{}, &bytes.Buffer{}, func(kubernetes.Options) (Reader, kubernetes.Target, error) {
			t.Fatal("factory called for invalid request")
			return nil, kubernetes.Target{}, nil
		})
		if code != test.code {
			t.Errorf("args=%v code=%d want=%d", test.args, code, test.code)
		}
	}
}

func TestCLIUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"delete", "pods"}, {"get", "secrets"}, {"get", "pods", "-o", "yaml"}, {"get", "pods", "--request-timeout", "0s"}, {"get", "pods", "a", "extra"}} {
		var stdout, stderr bytes.Buffer
		code := Run(args, &stdout, &stderr, func(kubernetes.Options) (Reader, kubernetes.Target, error) {
			t.Fatal("factory called for invalid command")
			return nil, kubernetes.Target{}, nil
		})
		if code != 2 {
			t.Errorf("args=%v code=%d stderr=%s", args, code, stderr.String())
		}
	}
	for _, args := range [][]string{{"--help"}, {"version"}} {
		if code := Run(args, &bytes.Buffer{}, &bytes.Buffer{}, nil); code != 0 {
			t.Errorf("args=%v code=%d", args, code)
		}
	}
}

func TestCLIErrorCodes(t *testing.T) {
	tests := []struct {
		err  error
		code int
	}{
		{apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "pod", errors.New("denied")), 3},
		{apierrors.NewUnauthorized("denied"), 3},
		{apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "pod"), 4},
		{fmt.Errorf("request: %w", context.DeadlineExceeded), 5},
		{errors.New("other"), 1},
	}
	for _, test := range tests {
		var stderr bytes.Buffer
		code := Run([]string{"get", "pod", "pod"}, &bytes.Buffer{}, &stderr, func(kubernetes.Options) (Reader, kubernetes.Target, error) {
			return fakeReader{err: test.err}, kubernetes.Target{Context: "ctx", Namespace: "ns"}, nil
		})
		if code != test.code || !strings.Contains(stderr.String(), "Target:") || !strings.Contains(stderr.String(), "Error:") {
			t.Errorf("error=%v code=%d stderr=%s", test.err, code, stderr.String())
		}
	}
}

type waitingReader struct{}

func (waitingReader) Get(ctx context.Context, _, _, _ string) ([]kubernetes.Resource, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCLITimeout(t *testing.T) {
	code := Run([]string{"get", "pods", "--request-timeout", "1ms"}, &bytes.Buffer{}, &bytes.Buffer{}, func(kubernetes.Options) (Reader, kubernetes.Target, error) {
		return waitingReader{}, kubernetes.Target{Context: "ctx", Namespace: "ns"}, nil
	})
	if code != 5 {
		t.Fatalf("got code %d, want timeout code 5", code)
	}
}

func TestCLIWithKubeconfigAndHTTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/namespaces/team/pods/web" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"web","namespace":"team","annotations":{"token":"do-not-print"}},"spec":{"containers":[]},"status":{"phase":"Running"}}`)
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config")
	config := clientcmdapi.Config{
		CurrentContext: "local",
		Clusters:       map[string]*clientcmdapi.Cluster{"local": {Server: server.URL}},
		Contexts:       map[string]*clientcmdapi.Context{"local": {Cluster: "local", Namespace: "team"}},
	}
	if err := clientcmd.WriteToFile(config, configPath); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--kubeconfig", configPath, "get", "pod", "web", "-o", "json"}, &stdout, &stderr, nil)
	if code != 0 || !strings.Contains(stdout.String(), `"name":"web"`) || strings.Contains(stdout.String(), "do-not-print") || !strings.Contains(stderr.String(), "context=local namespace=team") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
