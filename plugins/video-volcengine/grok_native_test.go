package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeGrokContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"text", `{"model":"grok-imagine-video","prompt":"test"}`, true},
		{"reference", `{"model":"grok-imagine-video","prompt":"test","duration":10,"reference_images":[{"url":"https://example.com/a.png"}]}`, true},
		{"single", `{"model":"grok-imagine-video-1.5","image":{"url":"https://example.com/a.png"},"resolution":"1080p"}`, true},
		{"inline", `{"model":"grok-imagine-video","prompt":"test","reference_images":["data:image/png;base64,AAAA"]}`, false},
		{"reference_duration", `{"model":"grok-imagine-video","prompt":"test","duration":11,"reference_images":["https://example.com/a.png"]}`, false},
		{"requires_image", `{"model":"grok-imagine-video-1.5","prompt":"test"}`, false},
		{"preview_not_renamed", `{"model":"grok-imagine-video-1.5-preview","prompt":"test"}`, false},
		{"no_15_references", `{"model":"grok-imagine-video-1.5","reference_images":["https://example.com/a.png"]}`, false},
		{"mixed", `{"model":"grok-imagine-video","image":"https://example.com/a.png","reference_images":["https://example.com/b.png"]}`, false},
		{"duration_fraction", `{"model":"grok-imagine-video","prompt":"test","duration":6.5}`, false},
		{"resolution", `{"model":"grok-imagine-video","prompt":"test","resolution":"1080p"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input map[string]any
			if err := json.Unmarshal([]byte(tc.body), &input); err != nil {
				t.Fatal(err)
			}
			_, err := normalizeNativeGrokPayload(input)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestNativeGrokCreateAndPoll(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodPost {
			if r.URL.Path != "/v1/videos/generations" {
				t.Errorf("wrong path %s", r.URL.Path)
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["reference_images"] == nil || body["images"] != nil {
				t.Errorf("incorrect image fields")
			}
			writeJSON(w, 202, map[string]any{"request_id": "test-task"})
		} else {
			if r.URL.Path != "/v1/videos/generations/test-task" {
				t.Errorf("wrong poll path %s", r.URL.Path)
			}
			writeJSON(w, 200, map[string]any{"request_id": "test-task", "status": "done", "video": map[string]any{"url": "https://example.com/a.mp4"}})
		}
	}))
	defer upstream.Close()
	srv := newServer(config{arkBaseURL: "https://ark.example.com/api/v3", defaultProvider: providerVolcengineArk, maxResponseBytes: defaultMaxResponseSize, allowedDownloadHosts: []string{".example.com"}})
	for _, body := range []string{`{"model":"grok-imagine-video","prompt":"test","reference_images":["https://example.com/a.png"]}`, `{"model":"grok-imagine-video","prompt":"test","reference_images":["data:image/png;base64,AAAA"]}`} {
		req := httptest.NewRequest("POST", "/plugin/v1/videos", strings.NewReader(body))
		req.Header.Set(headerProvider, "grok_native")
		req.Header.Set(headerProviderBaseURL, upstream.URL)
		req.Header.Set("Authorization", "Bearer test")
		p, err := srv.providerConfig(req)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		srv.createCompatibleVideo(w, req, p)
		if strings.Contains(body, "data:image") {
			if w.Code != 400 {
				t.Fatalf("invalid input: %d", w.Code)
			}
		} else if w.Code != 200 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(body, "data:image") {
			req.SetPathValue("id", "test-task")
			req.Method = "GET"
			w = httptest.NewRecorder()
			srv.getCompatibleVideo(w, req, p)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "completed") || !strings.Contains(w.Body.String(), "a.mp4") {
				t.Fatalf("poll: %s", w.Body.String())
			}
		}
	}
	if calls != 2 {
		t.Fatalf("unexpected upstream calls %d", calls)
	}
}
