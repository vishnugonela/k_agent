package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
)

func TestReadOnlyAllResources(t *testing.T) {
	listKinds := make(map[schema.GroupVersionResource]string)
	objects := make([]runtime.Object, 0, len(resources))
	for _, spec := range resources {
		listKinds[spec.resource] = spec.kind + "List"
		object := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": spec.resource.GroupVersion().String(), "kind": spec.kind,
			"metadata": map[string]interface{}{"name": "example", "namespace": "selected", "annotations": map[string]interface{}{"password": "hidden-value"}},
			"spec":     map[string]interface{}{"secret": "hidden-value"},
		}}
		objects = append(objects, object)
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)
	reader := NewReader(client)
	for kind := range resources {
		for _, name := range []string{"", "example"} {
			items, err := reader.Get(context.Background(), "selected", kind, name)
			if err != nil || len(items) != 1 || items[0].Name != "example" {
				t.Fatalf("get %s %q: items=%+v error=%v", kind, name, items, err)
			}
			encoded, err := json.Marshal(items)
			if err != nil || strings.Contains(string(encoded), "hidden-value") {
				t.Fatalf("unexpected output %s: %v", encoded, err)
			}
		}
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" && action.GetVerb() != "list" {
			t.Errorf("unexpected write action: %s", action.GetVerb())
		}
		if action.GetNamespace() != "selected" {
			t.Errorf("unexpected namespace: %s", action.GetNamespace())
		}
	}
	if _, err := reader.Get(context.Background(), "selected", "secrets", ""); err == nil {
		t.Fatal("secrets must not be supported")
	}
}

func TestListPagination(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/namespaces/ns/pods" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.URL.Query().Get("limit") != "200" || (calls == 2 && request.URL.Query().Get("continue") != "next") {
			t.Errorf("unexpected query: %s", request.URL.RawQuery)
		}
		continuation := ""
		if calls == 1 {
			continuation = "next"
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"apiVersion":"v1","kind":"PodList","metadata":{"continue":%q},"items":[{"metadata":{"name":"pod","namespace":"ns"}}]}`, continuation)
	}))
	defer server.Close()
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	items, err := NewReader(client).Get(context.Background(), "ns", "pods", "")
	if err != nil || calls != 2 || len(items) != 2 {
		t.Fatalf("items=%v calls=%d error=%v", items, calls, err)
	}
}

func TestReadErrors(t *testing.T) {
	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	denied := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "test", errors.New("not allowed"))
	client.PrependReactor("get", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, denied
	})
	_, err := NewReader(client).Get(context.Background(), "ns", "pod", "test")
	if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("expected contextual forbidden error, got %v", err)
	}
}

func TestPodSummary(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": "pod", "namespace": "ns"},
		"spec":     map[string]interface{}{"containers": []interface{}{map[string]interface{}{}, map[string]interface{}{}}},
		"status": map[string]interface{}{"phase": "Running", "containerStatuses": []interface{}{
			map[string]interface{}{"ready": true, "restartCount": int64(3)},
			map[string]interface{}{"ready": false, "restartCount": int64(2)},
		}},
	}}
	result := summarize("Pod", object)
	if result.Status != "Running" || result.Ready != "1/2" || result.Restarts == nil || *result.Restarts != 5 {
		t.Fatalf("unexpected summary: %+v", result)
	}
}
