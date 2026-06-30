package prom

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	baseURL      *url.URL
	queryTimeout time.Duration
	headers      map[string]string
	httpClient   *http.Client
}

type Sample struct {
	Metric    map[string]string
	Timestamp time.Time
	Value     float64
}

type SampleStream struct {
	Metric map[string]string
	Values []Point
}

type Point struct {
	Timestamp time.Time
	Value     float64
}

type apiEnvelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  string          `json:"error"`
}

type vectorData struct {
	ResultType string         `json:"resultType"`
	Result     []vectorResult `json:"result"`
}

type vectorResult struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

type matrixData struct {
	ResultType string         `json:"resultType"`
	Result     []matrixResult `json:"result"`
}

type matrixResult struct {
	Metric map[string]string `json:"metric"`
	Values [][]any           `json:"values"`
}

func New(base string, timeout time.Duration, headers map[string]string, insecure bool) (*Client, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse prometheus url: %w", err)
	}
	return &Client{
		baseURL:      parsed,
		queryTimeout: timeout,
		headers:      headers,
		httpClient: &http.Client{
			Timeout: timeout + 5*time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
			},
		},
	}, nil
}

func (c *Client) Query(ctx context.Context, expr string, ts time.Time) ([]Sample, error) {
	params := url.Values{}
	params.Set("query", expr)
	if !ts.IsZero() {
		params.Set("time", strconv.FormatFloat(float64(ts.Unix()), 'f', -1, 64))
	}
	if c.queryTimeout > 0 {
		params.Set("timeout", c.queryTimeout.String())
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "/api/v1/query", RawQuery: params.Encode()})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var envelope apiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode prometheus response: %w", err)
	}
	if envelope.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s", envelope.Error)
	}

	var data vectorData
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("decode prometheus vector data: %w", err)
	}

	samples := make([]Sample, 0, len(data.Result))
	for _, item := range data.Result {
		timestamp, value, err := parsePoint(item.Value)
		if err != nil {
			return nil, err
		}
		samples = append(samples, Sample{
			Metric:    item.Metric,
			Timestamp: timestamp,
			Value:     value,
		})
	}
	return samples, nil
}

func (c *Client) QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration) ([]SampleStream, error) {
	params := url.Values{}
	params.Set("query", expr)
	params.Set("start", strconv.FormatFloat(float64(start.Unix()), 'f', -1, 64))
	params.Set("end", strconv.FormatFloat(float64(end.Unix()), 'f', -1, 64))
	params.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))
	if c.queryTimeout > 0 {
		params.Set("timeout", c.queryTimeout.String())
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "/api/v1/query_range", RawQuery: params.Encode()})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var envelope apiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode prometheus range response: %w", err)
	}
	if envelope.Status != "success" {
		return nil, fmt.Errorf("prometheus range query failed: %s", envelope.Error)
	}

	var data matrixData
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("decode prometheus matrix data: %w", err)
	}

	streams := make([]SampleStream, 0, len(data.Result))
	for _, result := range data.Result {
		points := make([]Point, 0, len(result.Values))
		for _, raw := range result.Values {
			timestamp, value, err := parsePoint(raw)
			if err != nil {
				return nil, err
			}
			points = append(points, Point{Timestamp: timestamp, Value: value})
		}
		streams = append(streams, SampleStream{
			Metric: result.Metric,
			Values: points,
		})
	}
	return streams, nil
}

func parsePoint(raw []any) (time.Time, float64, error) {
	if len(raw) != 2 {
		return time.Time{}, 0, fmt.Errorf("unexpected prometheus point shape")
	}
	var seconds float64
	switch value := raw[0].(type) {
	case float64:
		seconds = value
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return time.Time{}, 0, err
		}
		seconds = parsed
	default:
		return time.Time{}, 0, fmt.Errorf("unexpected prometheus timestamp type")
	}
	stringValue, ok := raw[1].(string)
	if !ok {
		return time.Time{}, 0, fmt.Errorf("unexpected prometheus point value type")
	}
	value, err := strconv.ParseFloat(stringValue, 64)
	if err != nil {
		return time.Time{}, 0, err
	}
	return time.Unix(int64(seconds), 0).UTC(), value, nil
}
