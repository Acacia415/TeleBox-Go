package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientJSONAndDefaults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.UserAgent() != "test-agent" {
			t.Errorf("User-Agent = %q", request.UserAgent())
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := New(Config{
		Timeout:          time.Second,
		MaxConcurrent:    2,
		MaxResponseBytes: 1024,
		UserAgent:        "test-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var payload struct {
		OK bool `json:"ok"`
	}
	response, err := client.JSON(context.Background(), Request{URL: server.URL}, &payload)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !payload.OK {
		t.Fatalf("response = %+v, payload = %+v", response, payload)
	}
}

func TestClientRejectsLargeAndUnsafeResponses(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("12345"))
	}))
	defer server.Close()
	client, err := New(Config{
		Timeout:          time.Second,
		MaxConcurrent:    1,
		MaxResponseBytes: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.Do(context.Background(), Request{URL: server.URL}); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Do() error = %v, want ErrResponseTooLarge", err)
	}
	for _, unsafe := range []string{"file:///tmp/a", "https://user:pass@example.com"} {
		if _, err := client.Do(context.Background(), Request{URL: unsafe}); err == nil {
			t.Fatalf("Do(%q) accepted unsafe URL", unsafe)
		}
	}
}

func TestRequestTimeoutOverridesClientDefault(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(40 * time.Millisecond)
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()
	client, err := New(Config{
		Timeout:          10 * time.Millisecond,
		MaxConcurrent:    1,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.Do(context.Background(), Request{
		URL:     server.URL,
		Timeout: 200 * time.Millisecond,
	}); err != nil {
		t.Fatalf("request with timeout override failed: %v", err)
	}
}
