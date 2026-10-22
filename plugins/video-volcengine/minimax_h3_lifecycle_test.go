package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiniMaxH3CreateRestartQueryDownload(t *testing.T) {
	for _, mode := range []string{"same-origin", "external-cdn", "json-content"} {
		t.Run(mode, func(t *testing.T) {
			creates, downloads := 0, 0
			cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downloads++
				if r.Header.Get("Authorization") != "" || r.Header.Get("X-Sub2-Plugin-Token") != "" {
					t.Error("CDN received credentials")
				}
				if r.Header.Get("Range") != "bytes=0-3" {
					t.Error("CDN range missing")
				}
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Content-Range", "bytes 0-3/4")
				w.WriteHeader(206)
				io.WriteString(w, "clip")
			}))
			defer cdn.Close()
			var relay *httptest.Server
			relay = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer upstream-test" {
					t.Error("provider authorization missing")
				}
				switch r.URL.Path {
				case "/v1/videos":
					creates++
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload["size"] != "2K" || payload["resolution"] != nil || payload["workflow_id"] != "text-to-video" {
						t.Errorf("incorrect payload: %#v", payload)
					}
					writeJSON(w, 200, map[string]any{"id": "wrong-id", "data": map[string]any{"task_id": "h3-task", "status": "queued"}})
				case "/v1/videos/h3-task":
					result := map[string]any{"task_id": "h3-task", "status": "completed"}
					if mode != "json-content" {
						target := relay.URL + "/files/video.mp4"
						if mode == "external-cdn" {
							target = cdn.URL + "/clip.mp4"
						}
						result["result_url"] = target
						result["video_url"] = "https://wrong.example.com/incorrect.mp4"
					}
					writeJSON(w, 200, map[string]any{"data": result})
				case "/v1/videos/h3-task/content":
					writeJSON(w, 200, map[string]any{"data": map[string]any{"metadata": map[string]any{"url": relay.URL + "/files/video.mp4"}}})
				case "/files/video.mp4":
					downloads++
					if r.Header.Get("Range") != "bytes=0-3" {
						t.Error("provider range missing")
					}
					w.Header().Set("Content-Type", "video/mp4")
					w.Header().Set("Content-Range", "bytes 0-3/4")
					w.WriteHeader(206)
					io.WriteString(w, "clip")
				default:
					t.Errorf("unexpected upstream path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer relay.Close()
			cfg := config{taskRouteDir: t.TempDir(), maxResponseBytes: defaultMaxResponseSize, allowedDownloadHosts: []string{"127.0.0.1"}}
			srv := newServer(cfg)
			srv.client = relay.Client()
			request := func(method, path, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, "http://plugin/plugin/v1/videos"+path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer upstream-test")
				req.Header.Set("X-Sub2-Video-Account-ID", "289")
				req.Header.Set(headerProvider, providerOpenAICompat)
				req.Header.Set(headerProviderBaseURL, relay.URL)
				req.Header.Set(headerStatusPath, "/wrong/{id}")
				req.Header.Set(headerContentPath, "/wrong/{id}/content")
				req.Header.Set("Range", "bytes=0-3")
				w := httptest.NewRecorder()
				srv.routes().ServeHTTP(w, req)
				return w
			}
			created := request("POST", "", `{"model":"MiniMax-H3","prompt":"test","size":"2K","aspect_ratio":"16:9"}`)
			if created.Code != 200 || !strings.Contains(created.Body.String(), `"id":"h3-task"`) {
				t.Fatalf("create: %d %s", created.Code, created.Body.String())
			}
			srv = newServer(cfg)
			srv.client = relay.Client() // Simulates another process using the shared task volume.
			status := request("GET", "/h3-task", "")
			if status.Code != 200 || !strings.Contains(status.Body.String(), `"model":"MiniMax-H3"`) {
				t.Fatalf("status: %d %s", status.Code, status.Body.String())
			}
			content := request("GET", "/h3-task/content", "")
			if content.Code != 206 || content.Body.String() != "clip" || content.Header().Get("Content-Range") != "bytes 0-3/4" {
				t.Fatalf("download: %d %s", content.Code, content.Body.String())
			}
			if creates != 1 || downloads != 1 {
				t.Fatalf("calls create=%d download=%d", creates, downloads)
			}
		})
	}
}

func TestMiniMaxH3ResponseURLPriority(t *testing.T) {
	fields := []string{"result_url", "video_url", "url", "metadata.url"}
	for i, field := range fields {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%v", field, wrapped), func(t *testing.T) {
				obj := map[string]any{}
				for j := i; j < len(fields); j++ {
					url := fmt.Sprintf("https://cdn.example.com/%d.mp4", j)
					if j == 3 {
						obj["metadata"] = map[string]any{"url": url}
					} else {
						obj[fields[j]] = url
					}
				}
				if wrapped {
					obj = map[string]any{"data": obj}
				}
				b, _ := json.Marshal(obj)
				if got := miniMaxH3VideoURL(b); got != fmt.Sprintf("https://cdn.example.com/%d.mp4", i) {
					t.Fatal(got)
				}
			})
		}
	}
}
