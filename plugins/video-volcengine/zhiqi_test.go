package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type zhiqiTestTransport func(*http.Request) (*http.Response, error)

func (f zhiqiTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestZhiqiPayloadContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"minimal", `{"model":"seedance-2.0","prompt":"test"}`, true},
		{"media", `{"model":"seedance-2.0","image":{"url":"https://example.com/a.png"},"videos":["https://example.com/a.mp4"],"audios":["https://example.com/a.mp3"],"generate_audio":true}`, true},
		{"equivalent_seconds", `{"duration":7,"seconds":"7"}`, true},
		{"empty_alias", `{"reference_image_urls":[],"images":["https://example.com/a.png"]}`, true},
		{"all_options", `{"duration":7,"size":"1280x720","resolution":"720p","aspect_ratio":"16:9","first_frame_image_url":"https://example.com/first.png","last_frame_image_url":"https://example.com/last.png","negative_prompt":"blur","return_last_frame":true,"seed":42,"n":1,"bypass_face_check":false,"grid_strength":0.5,"strength":5,"auto_face_mask":true}`, true},
		{"conflict", `{"image":"https://example.com/a.png","image_url":"https://example.com/a.png"}`, false},
		{"duration_conflict", `{"duration":7,"seconds":8}`, false},
		{"fraction", `{"duration":7.5}`, false},
		{"NaN", `{"duration":"NaN"}`, false},
		{"unknown", `{"watermark":true}`, false},
		{"base64", `{"images":["data:image/png;base64,AAAA"]}`, false},
		{"local", `{"input_reference":"C:/local/a.png"}`, false},
		{"not_array", `{"reference_audio_urls":"https://example.com/a.mp3"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal([]byte(tc.body), &body); err != nil {
				t.Fatal(err)
			}
			out, err := normalizeZhiqiVideoPayload(body)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.name == "media" && (out["input_reference"] != "https://example.com/a.png" || !reflect.DeepEqual(out["reference_video_urls"], []string{"https://example.com/a.mp4"})) {
				t.Fatalf("wrong fields: %#v", out)
			}
		})
	}
}

func zhiqiTestRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(headerProvider, providerWaninter)
	r.Header.Set(headerProviderBaseURL, "https://zhiqiapi.com")
	r.Header.Set(headerCreatePath, "/v1/contents/generations/tasks")
	r.Header.Set(headerStatusPath, "/kling/v1/videos/text2video/{id}")
	r.Header.Set(headerContentPath, "/kling/v1/videos/text2video/{id}")
	r.Header.Set("Authorization", "Bearer test-only-key")
	return r
}

func TestZhiqiUsesOnlyCanonicalPaths(t *testing.T) {
	srv := newServer(config{maxResponseBytes: defaultMaxResponseSize, taskRouteDir: t.TempDir()})
	var paths []string
	srv.client.Transport = zhiqiTestTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		status, body, contentType := 200, `{"id":"vidtask_test","status":"succeeded","data":[{}, {"url":"https://vfile.zhiqizhineng.com/result.mp4"}]}`, "application/json"
		if r.Method == "POST" {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["input_reference"] != "https://example.com/a.png" || payload["image"] != nil {
				t.Error("noncanonical reference fields")
			}
			body = `{"id":"vidtask_test","status":"queued"}`
		} else if r.URL.Host == "vfile.zhiqizhineng.com" {
			body, contentType = "video-bytes", "video/mp4"
		}
		if strings.Contains(r.URL.Path, "missing") {
			status, body = 404, `{"error":{"message":"not found"}}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	for _, item := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/plugin/v1/videos", `{"model":"seedance-2.0","prompt":"test","image":{"url":"https://example.com/a.png"}}`, 200},
		{"GET", "/plugin/v1/videos/vidtask_test", "", 200},
		{"GET", "/plugin/v1/videos/vidtask_test/content", "", 200},
		{"GET", "/plugin/v1/videos/missing", "", 404},
	} {
		response := httptest.NewRecorder()
		srv.routes().ServeHTTP(response, zhiqiTestRequest(item.method, item.path, item.body))
		if response.Code != item.status {
			t.Fatalf("%s: %d %s", item.path, response.Code, response.Body.String())
		}
	}
	expected := []string{"POST /v1/videos", "GET /v1/videos/vidtask_test", "GET /v1/videos/vidtask_test", "GET /result.mp4", "GET /v1/videos/missing"}
	if !reflect.DeepEqual(paths, expected) {
		t.Fatalf("unexpected provider requests: %#v", paths)
	}
}

func TestZhiqiStatusAndContentStates(t *testing.T) {
	for _, tc := range []struct{ status, expected string }{{"queued", "pending"}, {"in_progress", "pending"}, {"succeeded", "done"}, {"failed", "failed"}, {"expired", "expired"}} {
		body := []byte(`{"status":"` + tc.status + `","progress":"99%","data":[{}, {"url":"https://vfile.zhiqizhineng.com/result.mp4"}],"error":{"code":"media_storage_failed","message":"Storage failed"}}`)
		result := normalizeCompatibleTask(body, "existing")
		if result["status"] != tc.expected || result["progress"] != "99%" || result["video"].(map[string]any)["url"] == "" {
			t.Fatalf("wrong normalization: %#v", result)
		}
		if result["error"].(map[string]string)["code"] != "media_storage_failed" {
			t.Fatal("lost error code")
		}
		if tc.status == "succeeded" {
			continue
		}
		srv := newServer(config{maxResponseBytes: defaultMaxResponseSize})
		calls := 0
		srv.client.Transport = zhiqiTestTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
		})
		response := httptest.NewRecorder()
		srv.routes().ServeHTTP(response, zhiqiTestRequest("GET", "/plugin/v1/videos/existing/content", ""))
		if response.Code != 409 || calls != 1 {
			t.Fatalf("non-success content fetched: %d calls=%d", response.Code, calls)
		}
	}
}

func TestZhiqiProviderScopeAndCachedPaths(t *testing.T) {
	for _, host := range []string{"https://zhiqiapi.com", "https://sub.zhiqiapi.com"} {
		if !isZhiqiBaseURL(host) {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"https://zhiqiapi.com.evil.test", "https://other.example"} {
		if isZhiqiBaseURL(host) {
			t.Fatal(host)
		}
	}
	srv := newServer(config{maxResponseBytes: defaultMaxResponseSize})
	request := zhiqiTestRequest("GET", "/", "")
	provider, err := srv.providerConfig(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.storeTaskRoute(request, provider, "existing", seedanceTaskRoute{StatusPath: seedanceStatusPath, ContentPath: seedanceStatusPath}); err != nil {
		t.Fatal(err)
	}
	if status, content := srv.videoTaskPaths(request, provider, "existing"); status != defaultStatusPath || content != defaultContentPath {
		t.Fatal("cached vendor path used")
	}
	if _, err := srv.validateDownloadURLForProvider("https://vfile.zhiqizhineng.com/result.mp4", provider); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.validateDownloadURLForProvider("https://vfile.zhiqizhineng.com/result.mp4", providerConfig{}); err == nil {
		t.Fatal("Zhiqi CDN allowed globally")
	}
}
