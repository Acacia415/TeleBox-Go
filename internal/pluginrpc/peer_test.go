package pluginrpc

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

type addRequest struct {
	A int `json:"a"`
	B int `json:"b"`
}

func TestPeerSupportsBidirectionalCalls(t *testing.T) {
	t.Parallel()

	hostReader, pluginWriter := io.Pipe()
	pluginReader, hostWriter := io.Pipe()
	defer hostReader.Close()
	defer hostWriter.Close()
	defer pluginReader.Close()
	defer pluginWriter.Close()

	var host *Peer
	host = New(hostReader, hostWriter, func(
		ctx context.Context,
		method string,
		params json.RawMessage,
	) (any, error) {
		switch method {
		case "host.double":
			var value int
			if err := json.Unmarshal(params, &value); err != nil {
				return nil, err
			}
			return value * 2, nil
		default:
			return nil, &RemoteError{Code: "unknown", Message: method}
		}
	})
	var plugin *Peer
	plugin = New(pluginReader, pluginWriter, func(
		ctx context.Context,
		method string,
		params json.RawMessage,
	) (any, error) {
		switch method {
		case "plugin.add_and_double":
			var request addRequest
			if err := json.Unmarshal(params, &request); err != nil {
				return nil, err
			}
			var doubled int
			if err := plugin.Call(ctx, "host.double", request.A+request.B, &doubled); err != nil {
				return nil, err
			}
			return doubled, nil
		default:
			return nil, &RemoteError{Code: "unknown", Message: method}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result int
	if err := host.Call(ctx, "plugin.add_and_double", addRequest{A: 2, B: 3}, &result); err != nil {
		t.Fatal(err)
	}
	if result != 10 {
		t.Fatalf("result = %d, want 10", result)
	}
}
