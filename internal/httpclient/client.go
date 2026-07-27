package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrResponseTooLarge = errors.New("HTTP response exceeds configured size limit")

type Config struct {
	Timeout          time.Duration
	MaxConcurrent    int
	MaxResponseBytes int64
	UserAgent        string
}

type Request struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

type Response struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// Client is the bounded HTTP surface shared by plugins. It prevents individual
// plugins from silently creating unbounded clients, hanging forever, or
// reading arbitrarily large response bodies.
type Client struct {
	client           *http.Client
	slots            chan struct{}
	maxResponseBytes int64
	userAgent        string
}

func New(cfg Config) (*Client, error) {
	if cfg.Timeout <= 0 {
		return nil, errors.New("HTTP timeout must be greater than zero")
	}
	if cfg.MaxConcurrent <= 0 {
		return nil, errors.New("HTTP max concurrency must be greater than zero")
	}
	if cfg.MaxResponseBytes <= 0 {
		return nil, errors.New("HTTP response limit must be greater than zero")
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = "TeleBox-Go/0.1"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = cfg.MaxConcurrent * 2
	transport.MaxIdleConnsPerHost = cfg.MaxConcurrent
	transport.IdleConnTimeout = 90 * time.Second

	result := &Client{
		slots:            make(chan struct{}, cfg.MaxConcurrent),
		maxResponseBytes: cfg.MaxResponseBytes,
		userAgent:        userAgent,
	}
	result.client = &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			return validateURL(request.URL)
		},
	}
	return result, nil
}

func (c *Client) Do(ctx context.Context, request Request) (Response, error) {
	if ctx == nil {
		return Response{}, errors.New("HTTP context is required")
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}
	parsed, err := url.Parse(request.URL)
	if err != nil {
		return Response{}, fmt.Errorf("parse HTTP URL: %w", err)
	}
	if err := validateURL(parsed); err != nil {
		return Response{}, err
	}

	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}

	httpRequest, err := http.NewRequestWithContext(ctx, method, parsed.String(), bytes.NewReader(request.Body))
	if err != nil {
		return Response{}, fmt.Errorf("create HTTP request: %w", err)
	}
	httpRequest.Header = request.Headers.Clone()
	if httpRequest.Header == nil {
		httpRequest.Header = make(http.Header)
	}
	if httpRequest.Header.Get("User-Agent") == "" {
		httpRequest.Header.Set("User-Agent", c.userAgent)
	}

	httpResponse, err := c.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("perform HTTP request: %w", err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.ContentLength > c.maxResponseBytes {
		return Response{}, fmt.Errorf("%w: content-length=%d limit=%d",
			ErrResponseTooLarge,
			httpResponse.ContentLength,
			c.maxResponseBytes,
		)
	}
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, c.maxResponseBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("read HTTP response: %w", err)
	}
	if int64(len(body)) > c.maxResponseBytes {
		return Response{}, fmt.Errorf("%w: limit=%d", ErrResponseTooLarge, c.maxResponseBytes)
	}
	return Response{
		StatusCode: httpResponse.StatusCode,
		Headers:    httpResponse.Header.Clone(),
		Body:       body,
	}, nil
}

func (c *Client) JSON(ctx context.Context, request Request, target any) (Response, error) {
	if target == nil {
		return Response{}, errors.New("JSON target is required")
	}
	response, err := c.Do(ctx, request)
	if err != nil {
		return Response{}, err
	}
	if err := json.Unmarshal(response.Body, target); err != nil {
		return response, fmt.Errorf("decode HTTP JSON response: %w", err)
	}
	return response, nil
}

func (c *Client) Close() {
	c.client.CloseIdleConnections()
}

func validateURL(parsed *url.URL) error {
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("unsupported HTTP URL scheme %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return errors.New("HTTP URL host is required")
	}
	if parsed.User != nil {
		return errors.New("HTTP URL user information is not allowed")
	}
	return nil
}
