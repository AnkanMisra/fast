package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// fallbackToken is used when we can't extract a fresh token from the fast.com
// JavaScript bundle. It rarely changes, so this is usually good enough.
const fallbackToken = "YXNkZmFzZGxmbnNkYWZoYXNkZmhrYWxm"

const uploadPayloadBytes = 512 * 1024
const uploadConnections = 10

const requestTimeout = 15 * time.Second

var httpClient = &http.Client{Timeout: requestTimeout}

var (
	scriptExpr = regexp.MustCompile(`app-[a-z0-9]+\.js`)
	tokenExpr  = regexp.MustCompile(`token:"(\w+)"`)
	rangeExpr  = regexp.MustCompile(`/range/[^/]*`)
)

// token extracts the API token from the fast.com JavaScript bundle. fast.com
// embeds it in a script tag, so we fetch the page, find the script and pull the
// token out of it.
func token() string {
	page, err := get("https://fast.com/")
	if err != nil {
		return fallbackToken
	}

	script, err := get("https://fast.com/" + scriptExpr.FindString(string(page)))
	if err != nil {
		return fallbackToken
	}

	match := tokenExpr.FindSubmatch(script)
	if len(match) < 2 {
		return fallbackToken
	}
	return string(match[1])
}

// targets asks fast.com for count URLs to download from. fast.com is powered by
// Netflix, so these point at the Netflix Open Connect servers nearest to us.
func targets(count int) ([]string, error) {
	url := fmt.Sprintf("https://api.fast.com/netflix/speedtest/v2?https=true&token=%s&urlCount=%d", token(), count)
	body, err := get(url)
	if err != nil {
		return nil, err
	}

	var response struct {
		Targets []struct {
			URL string `json:"url"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	urls := make([]string, len(response.Targets))
	for i, target := range response.Targets {
		urls[i] = target.URL
	}
	return urls, nil
}

// download repeatedly downloads from url until the context is cancelled, adding
// the number of bytes it reads to total as it goes. We run a few of these in
// parallel to saturate the connection.
func download(ctx context.Context, url string, total *atomic.Int64) {
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return
		}

		_, _ = io.Copy(counter{total}, resp.Body)
		_ = resp.Body.Close()
	}
}

func upload(ctx context.Context, rawURL string, total *atomic.Int64) {
	uploadURL, err := uploadURL(rawURL)
	if err != nil {
		return
	}

	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			uploadURL,
			io.NopCloser(io.LimitReader(zeroReader{}, uploadPayloadBytes)),
		)
		if err != nil {
			return
		}

		req.ContentLength = uploadPayloadBytes
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := httpClient.Do(req)
		if err != nil {
			return
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			total.Add(uploadPayloadBytes)
		}
	}
}

// counter is an io.Writer that keeps a running total of how many bytes have
// been written to it, without keeping any of them around.
type counter struct {
	total *atomic.Int64
}

func (c counter) Write(p []byte) (int, error) {
	c.total.Add(int64(len(p)))
	return len(p), nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// get performs an HTTP GET request and returns the response body.
func get(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	return io.ReadAll(resp.Body)
}

func uploadURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	if rangeExpr.MatchString(parsed.Path) {
		parsed.Path = rangeExpr.ReplaceAllString(parsed.Path, "/range/0-0")
		return parsed.String(), nil
	}

	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/range/0-0"
	return parsed.String(), nil
}
