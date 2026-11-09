package kubernetes

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type Resource struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status,omitempty"`
	Ready     string `json:"ready,omitempty"`
	Restarts  *int64 `json:"restarts,omitempty"`
}

type resourceSpec struct {
	resource schema.GroupVersionResource
	kind     string
}

var resources = map[string]resourceSpec{
	"pods":         {schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "Pod"},
	"deployments":  {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "Deployment"},
	"statefulsets": {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, "StatefulSet"},
	"daemonsets":   {schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, "DaemonSet"},
	"jobs":         {schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, "Job"},
	"services":     {schema.GroupVersionResource{Version: "v1", Resource: "services"}, "Service"},
}

var aliases = map[string]string{
	"pod": "pods", "deployment": "deployments", "statefulset": "statefulsets",
	"daemonset": "daemonsets", "job": "jobs", "service": "services",
}

func ValidateKind(kind string) error {
	_, err := lookup(kind)
	return err
}

func lookup(kind string) (resourceSpec, error) {
	kind = strings.ToLower(kind)
	if plural, ok := aliases[kind]; ok {
		kind = plural
	}
	spec, ok := resources[kind]
	if !ok {
		return resourceSpec{}, fmt.Errorf("unsupported resource %q (choose pods, deployments, statefulsets, daemonsets, jobs, or services)", kind)
	}
	return spec, nil
}

type Reader struct {
	client dynamic.Interface
}

func NewReader(client dynamic.Interface) *Reader {
	return &Reader{client: client}
}

func (reader *Reader) Get(ctx context.Context, namespace, kind, name string) ([]Resource, error) {
	spec, err := lookup(kind)
	if err != nil {
		return nil, err
	}
	resource := reader.client.Resource(spec.resource).Namespace(namespace)
	if name != "" {
		object, err := resource.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get %s %q in namespace %q: %w", spec.resource.Resource, name, namespace, err)
		}
		return []Resource{summarize(spec.kind, object)}, nil
	}
	result := make([]Resource, 0)
	options := metav1.ListOptions{Limit: 200}
	for {
		list, err := resource.List(ctx, options)
		if err != nil {
			return nil, fmt.Errorf("list %s in namespace %q: %w", spec.resource.Resource, namespace, err)
		}
		for index := range list.Items {
			result = append(result, summarize(spec.kind, &list.Items[index]))
		}
		if list.GetContinue() == "" {
			return result, nil
		}
		options.Continue = list.GetContinue()
	}
}

func number(object *unstructured.Unstructured, fields ...string) int64 {
	value, _, _ := unstructured.NestedInt64(object.Object, fields...)
	return value
}

func summarize(kind string, object *unstructured.Unstructured) Resource {
	result := Resource{Kind: kind, Name: object.GetName(), Namespace: object.GetNamespace()}
	switch kind {
	case "Pod":
		result.Status, _, _ = unstructured.NestedString(object.Object, "status", "phase")
		containers, _, _ := unstructured.NestedSlice(object.Object, "spec", "containers")
		statuses, _, _ := unstructured.NestedSlice(object.Object, "status", "containerStatuses")
		var ready, restarts int64
		for _, item := range statuses {
			status, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if value, _, _ := unstructured.NestedBool(status, "ready"); value {
				ready++
			}
			count, _, _ := unstructured.NestedInt64(status, "restartCount")
			restarts += count
		}
		result.Ready = fmt.Sprintf("%d/%d", ready, len(containers))
		result.Restarts = &restarts
	case "Deployment", "StatefulSet":
		desired, found, _ := unstructured.NestedInt64(object.Object, "spec", "replicas")
		if !found {
			desired = 1
		}
		result.Ready = fmt.Sprintf("%d/%d", number(object, "status", "readyReplicas"), desired)
	case "DaemonSet":
		result.Ready = fmt.Sprintf("%d/%d", number(object, "status", "numberReady"), number(object, "status", "desiredNumberScheduled"))
	case "Job":
		result.Status = fmt.Sprintf("%d succeeded, %d failed", number(object, "status", "succeeded"), number(object, "status", "failed"))
	case "Service":
		result.Status, _, _ = unstructured.NestedString(object.Object, "spec", "type")
	}
	return result
}
