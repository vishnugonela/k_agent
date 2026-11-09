package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIToolRoundTrip(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authorization")
		}
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if string(payload["store"]) != "false" {
			t.Error("response storage must be disabled")
		}
		if requests == 1 {
			fmt.Fprint(writer, `{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"get_workloads","arguments":"{\"resource\":\"pods\",\"name\":\"web\"}"}]}`)
			return
		}
		if !strings.Contains(string(payload["input"]), `call_1`) || !strings.Contains(string(payload["input"]), `web`) {
			t.Errorf("missing tool output: %s", payload["input"])
		}
		fmt.Fprint(writer, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Pod team/web is Running."}]}]}`)
	}))
	defer server.Close()
	reader := &testReader{}
	answer, err := (OpenAIClient{APIKey: "test-key", Model: "test", endpoint: server.URL}).Ask(context.Background(), reader, "team", "Is web running?")
	if err != nil || answer != "Pod team/web is Running." || reader.calls != 1 || requests != 2 {
		t.Fatalf("answer=%q err=%v reads=%d requests=%d", answer, err, reader.calls, requests)
	}
}

func TestOpenAIIncompleteResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `{"status":"incomplete","output":[]}`)
	}))
	defer server.Close()
	_, err := (OpenAIClient{APIKey: "test-key", Model: "test", endpoint: server.URL}).Ask(context.Background(), &testReader{}, "team", "test")
	if err == nil || !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("unexpected error: %v", err)
	}
}
