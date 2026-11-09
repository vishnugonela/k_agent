package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vishnugonela/k_agent/internal/kubernetes"
)

type testReader struct {
	calls int
}

func (reader *testReader) Get(_ context.Context, namespace, kind, name string) ([]kubernetes.Resource, error) {
	reader.calls++
	if namespace != "team" || kind != "pods" || name != "web" {
		return nil, fmt.Errorf("unexpected read")
	}
	return []kubernetes.Resource{{Kind: "Pod", Namespace: namespace, Name: name, Status: "Running"}}, nil
}

func TestAskToolRoundTrip(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/api/chat" || request.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		var payload struct {
			Messages []message `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			fmt.Fprint(writer, `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"get_workloads","arguments":{"resource":"pods","name":"web"}}}]}}`)
			return
		}
		if len(payload.Messages) != 4 || payload.Messages[3].Role != "tool" || !strings.Contains(payload.Messages[3].Content, `"name":"web"`) {
			t.Errorf("unexpected messages: %+v", payload.Messages)
		}
		fmt.Fprint(writer, `{"message":{"role":"assistant","content":"Pod team/web is Running."}}`)
	}))
	defer server.Close()
	reader := &testReader{}
	answer, err := (Client{Endpoint: server.URL, Model: "test"}).Ask(context.Background(), reader, "team", "Is web running?")
	if err != nil || answer != "Pod team/web is Running." || reader.calls != 1 || requests != 2 {
		t.Fatalf("answer=%q err=%v reads=%d requests=%d", answer, err, reader.calls, requests)
	}
}

func TestToolRejectsUnsupportedCalls(t *testing.T) {
	reader := &testReader{}
	for _, test := range []struct{ name, args string }{
		{"delete_workloads", `{"resource":"pods","name":"web"}`},
		{"get_workloads", `{"resource":"secrets"}`},
		{"get_workloads", `{"resource":"pods","namespace":"other"}`},
		{"get_workloads", `{"resource":"pods"} {}`},
	} {
		var call toolCall
		call.Function.Name = test.name
		call.Function.Arguments = json.RawMessage(test.args)
		if result := runTool(context.Background(), reader, "team", call); !strings.Contains(result, "error") || reader.calls != 0 {
			t.Fatalf("result=%s reads=%d", result, reader.calls)
		}
	}
}

func TestAskToolCallLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"get_workloads","arguments":{"resource":"pods","name":"web"}}}]}}`)
	}))
	defer server.Close()
	reader := &testReader{}
	_, err := (Client{Endpoint: server.URL, Model: "test"}).Ask(context.Background(), reader, "team", "test")
	if err == nil || !strings.Contains(err.Error(), "tool call limit") || reader.calls != maxToolCalls {
		t.Fatalf("err=%v calls=%d", err, reader.calls)
	}
}

func TestAskHandlesProviderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "sensitive server detail", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, err := (Client{Endpoint: server.URL, Model: "test"}).Ask(context.Background(), &testReader{}, "team", "test")
	if err == nil || !strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "sensitive server detail") {
		t.Fatalf("unexpected error: %v", err)
	}
}
