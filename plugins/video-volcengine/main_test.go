package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testPluginServer(t *testing.T, ark http.Handler, secret string) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(ark)
	t.Cleanup(upstream.Close)
	srv := newServer(config{
		arkBaseURL:           upstream.URL,
		sharedSecret:         secret,
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".volces.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)
	return plugin
}

func TestCreateVideoConvertsPublicContract(t *testing.T) {
	ark := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/contents/generations/tasks" || r.Method != http.MethodPost {
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ark-secret" {
			t.Fatalf("authorization was not forwarded")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		content, ok := payload["content"].([]any)
		if !ok || len(content) != 3 {
			t.Fatalf("unexpected content: %#v", payload["content"])
		}
		first := content[1].(map[string]any)
		last := content[2].(map[string]any)
		if first["role"] != "first_frame" || last["role"] != "last_frame" {
			t.Fatalf("unexpected frame roles: %#v %#v", first, last)
		}
		if payload["ratio"] != "16:9" || payload["duration"] != float64(5) {
			t.Fatalf("unexpected video options: %#v", payload)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "cgt-123", "model": payload["model"], "status": "queued"})
	})
	plugin := testPluginServer(t, ark, "shared")

	body := `{"model":"doubao-seedance-2-5-260628","prompt":"camera move","images":["https://example.com/first.png",{"url":"https://example.com/last.png"}],"duration":5,"ratio":"16:9"}`
	req, _ := http.NewRequest(http.MethodPost, plugin.URL+"/plugin/v1/videos", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ark-secret")
	req.Header.Set("X-Sub2-Plugin-Token", "shared")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"id":"cgt-123"`) || !strings.Contains(string(data), `"status":"pending"`) {
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, data)
	}
}

func TestGetVideoNormalizesSucceededTaskAndUsage(t *testing.T) {
	ark := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/contents/generations/tasks/cgt-456" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "cgt-456", "model": "doubao-seedance-2-5-260628", "status": "succeeded",
			"duration": 5, "resolution": "1080p", "content": map[string]any{"video_url": "https://bucket.volces.com/video.mp4"},
			"usage": map[string]any{"completion_tokens": 246840, "total_tokens": 246840},
		})
	})
	plugin := testPluginServer(t, ark, "")

	req, _ := http.NewRequest(http.MethodGet, plugin.URL+"/plugin/v1/videos/cgt-456", nil)
	req.Header.Set("Authorization", "Bearer ark-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "done" {
		t.Fatalf("unexpected status: %#v", result)
	}
	usage := result["usage"].(map[string]any)
	if usage["completion_tokens"] != float64(246840) {
		t.Fatalf("usage was not preserved: %#v", usage)
	}
}

func TestPluginTokenIsRequiredWhenConfigured(t *testing.T) {
	plugin := testPluginServer(t, http.NotFoundHandler(), "shared")
	req, _ := http.NewRequest(http.MethodGet, plugin.URL+"/plugin/v1/manifest", nil)
	req.Header.Set("Authorization", "Bearer ark-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestDownloadURLAllowlist(t *testing.T) {
	srv := newServer(config{allowedDownloadHosts: []string{".volces.com", "img.mikuapi.org", "v15-kling.klingai.com", "ivd.bjstlx.com"}})
	if _, err := srv.validateDownloadURL("https://bucket.volces.com/video.mp4"); err != nil {
		t.Fatalf("expected allowed URL: %v", err)
	}
	if _, err := srv.validateDownloadURL("https://img.mikuapi.org/video.mp4"); err != nil {
		t.Fatalf("expected configured relay CDN URL: %v", err)
	}
	if _, err := srv.validateDownloadURL("https://v15-kling.klingai.com/video.mp4"); err != nil {
		t.Fatalf("expected configured Kling CDN URL: %v", err)
	}
	if _, err := srv.validateDownloadURL("https://ivd.bjstlx.com/video.mp4"); err != nil {
		t.Fatalf("expected configured Minimax H3 CDN URL: %v", err)
	}
	if _, err := srv.validateDownloadURL("https://volces.com.evil.example/video.mp4"); err == nil {
		t.Fatal("expected deceptive host to be rejected")
	}
	if _, err := srv.validateDownloadURL("https://img.mikuapi.org.evil.example/video.mp4"); err == nil {
		t.Fatal("expected deceptive relay CDN host to be rejected")
	}
	if _, err := srv.validateDownloadURL("https://v15-kling.klingai.com.evil.example/video.mp4"); err == nil {
		t.Fatal("expected deceptive Kling CDN host to be rejected")
	}
	if _, err := srv.validateDownloadURL("https://ivd.bjstlx.com.evil.example/video.mp4"); err == nil {
		t.Fatal("expected deceptive Minimax H3 CDN host to be rejected")
	}
	if _, err := srv.validateDownloadURL("http://bucket.volces.com/video.mp4"); err == nil {
		t.Fatal("expected insecure URL to be rejected")
	}
}

func TestOpenAICompatibleProviderUsesPerRequestUpstream(t *testing.T) {
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer relay-secret" {
			t.Fatalf("relay authorization was not forwarded")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]string{{"id": "relay-video-1"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos/generations":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["model"] != "relay-video-1" || payload["prompt"] != "a test" {
				t.Fatalf("unexpected relay payload: %#v", payload)
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": "relay-task-1", "status": "queued"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/relay-task-1":
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
				"id": "relay-task-1", "status": "completed", "video_url": "https://cdn.example.com/video.mp4",
			}})
		default:
			http.NotFound(w, r)
		}
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)

	srv := newServer(config{
		arkBaseURL:           "https://ark.example.com/api/v3",
		defaultProvider:      providerVolcengineArk,
		sharedSecret:         "shared",
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".example.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)

	request := func(method, path string, body io.Reader) *http.Request {
		req, err := http.NewRequest(method, plugin.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer relay-secret")
		req.Header.Set("X-Sub2-Plugin-Token", "shared")
		req.Header.Set(headerProvider, providerOpenAICompat)
		req.Header.Set(headerProviderBaseURL, relayServer.URL)
		return req
	}

	manifestReq := request(http.MethodGet, "/plugin/v1/manifest", nil)
	manifestResp, err := http.DefaultClient.Do(manifestReq)
	if err != nil {
		t.Fatal(err)
	}
	defer manifestResp.Body.Close()
	var manifest map[string]any
	if err := json.NewDecoder(manifestResp.Body).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["provider"] != providerOpenAICompat {
		t.Fatalf("unexpected provider: %#v", manifest)
	}

	createReq := request(http.MethodPost, "/plugin/v1/videos", strings.NewReader(`{"model":"relay-video-1","prompt":"a test"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	var created map[string]any
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created["id"] != "relay-task-1" || created["status"] != "pending" {
		t.Fatalf("unexpected create response: %#v", created)
	}

	statusReq := request(http.MethodGet, "/plugin/v1/videos/relay-task-1", nil)
	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var status map[string]any
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status["status"] != "done" {
		t.Fatalf("unexpected status response: %#v", status)
	}
	video := status["video"].(map[string]any)
	if video["url"] != "https://cdn.example.com/video.mp4" {
		t.Fatalf("unexpected video URL: %#v", video)
	}
}

func TestExtractCompatibleModelIDsSupportsRelayCatalogVariants(t *testing.T) {
	models, err := extractCompatibleModelIDs([]byte(`{
		"data": [
			{"model":"grok-video-1.5"},
			{"model_id":"kling-3.0-omni"}
		],
		"result": {
			"items": [
				{"slug":"hailuo-video-02"},
				{"name":"runway-gen4"},
				"custom-video-model"
			]
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"custom-video-model", "grok-video-1.5", "hailuo-video-02", "kling-3.0-omni", "runway-gen4"}
	if len(models) != len(want) {
		t.Fatalf("unexpected models: %#v", models)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Fatalf("unexpected models: %#v", models)
		}
	}
}

func TestOpenAICompatibleProviderUsesSeparateContentPath(t *testing.T) {
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer relay-secret" {
			t.Fatalf("relay authorization was not forwarded")
		}
		switch r.URL.Path {
		case "/v1/videos/task-1":
			writeJSON(w, http.StatusOK, map[string]any{"id": "task-1", "status": "completed"})
		case "/v1/videos/task-1/content":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("video-bytes"))
		default:
			http.NotFound(w, r)
		}
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)

	srv := newServer(config{
		arkBaseURL:           "https://ark.example.com/api/v3",
		defaultProvider:      providerVolcengineArk,
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".example.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)

	req, err := http.NewRequest(http.MethodGet, plugin.URL+"/plugin/v1/videos/task-1/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer relay-secret")
	req.Header.Set(headerProvider, providerOpenAICompat)
	req.Header.Set(headerProviderBaseURL, relayServer.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "video/mp4" || string(body) != "video-bytes" {
		t.Fatalf("unexpected content response: %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
}

func TestNormalizeCompatibleTaskSupportsNestedRelayResult(t *testing.T) {
	result := normalizeCompatibleTask([]byte(`{"data":{"result":{"id":"nested-task","status":"succeeded","output":{"video_url":"https://cdn.example.com/video.mp4"}}}}`), "fallback")
	if result["id"] != "nested-task" || result["status"] != "done" {
		t.Fatalf("unexpected normalized task: %#v", result)
	}
	video, ok := result["video"].(map[string]any)
	if !ok || video["url"] != "https://cdn.example.com/video.mp4" {
		t.Fatalf("unexpected normalized video: %#v", result)
	}
}

func TestWaninterProviderNormalizesImageObjects(t *testing.T) {
	var received map[string]any
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/videos" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "wan-task-1", "status": "queued"})
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)

	srv := newServer(config{
		arkBaseURL:           "https://ark.example.com/api/v3",
		defaultProvider:      providerVolcengineArk,
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".example.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)

	body := `{"model":"grok-video-1.5","prompt":"animate","images":[{"image_url":{"url":"https://example.com/reference.png"}}],"duration":6,"ratio":"16:9"}`
	req, err := http.NewRequest(http.MethodPost, plugin.URL+"/plugin/v1/videos", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer relay-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProvider, providerWaninter)
	req.Header.Set(headerProviderBaseURL, relayServer.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, data)
	}
	images, ok := received["images"].([]any)
	if !ok || len(images) != 1 || images[0] != "https://example.com/reference.png" {
		t.Fatalf("images were not normalized: %#v", received)
	}
	if received["seconds"] != "6" || received["aspect_ratio"] != "16:9" {
		t.Fatalf("Waninter video options were not normalized: %#v", received)
	}
	if _, ok := received["ratio"]; ok {
		t.Fatalf("legacy ratio field was not removed: %#v", received)
	}
}

func TestWaninterGrokDocumentedImageShapes(t *testing.T) {
	ordinary := map[string]any{"model": "grok-imagine-video", "images": []any{"https://example.com/person.png", map[string]any{"url": "https://example.com/clothing.png"}}}
	normalizeWaninterGrokImagePayload(ordinary)
	refs, ok := ordinary["reference_images"].([]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("ordinary Grok references were not preserved: %#v", ordinary)
	}
	if _, ok := refs[0].(map[string]string); !ok {
		t.Fatalf("ordinary Grok reference is not an object: %#v", refs[0])
	}
	preview := map[string]any{"model": "grok-imagine-video-1.5-preview", "reference_images": []any{map[string]any{"url": "https://example.com/start.png"}}}
	normalizeWaninterGrokImagePayload(preview)
	if preview["image_url"] != "https://example.com/start.png" {
		t.Fatalf("1.5 compatibility image_url string was not retained: %#v", preview)
	}
	for _, field := range []string{"image", "images", "reference_images"} {
		if _, ok := preview[field]; ok {
			t.Fatalf("1.5 %s field leaked into the legacy payload: %#v", field, preview)
		}
	}
}

func TestWaninterGrokProviderKeepsImageURLObjects(t *testing.T) {
	var received map[string]any
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/videos" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "grok-task-1", "status": "queued"})
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)

	srv := newServer(config{
		arkBaseURL:           "https://ark.example.com/api/v3",
		defaultProvider:      providerVolcengineArk,
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".example.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)

	body := `{"model":"grok-imagine-video","prompt":"animate","reference_images":[{"url":"https://example.com/person.png"},{"image_url":{"url":"https://example.com/style.png"}}],"duration":10,"ratio":"16:9"}`
	req, err := http.NewRequest(http.MethodPost, plugin.URL+"/plugin/v1/videos", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer relay-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProvider, providerWaninter)
	req.Header.Set(headerProviderBaseURL, relayServer.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, data)
	}

	images, ok := received["reference_images"].([]any)
	if !ok || len(images) != 2 {
		t.Fatalf("Grok images were not preserved as an array: %#v", received)
	}
	first, ok := images[0].(map[string]any)
	if !ok || first["url"] != "https://example.com/person.png" {
		t.Fatalf("first Grok image was not an ImageUrl object: %#v", images[0])
	}
	second, ok := images[1].(map[string]any)
	if !ok || second["url"] != "https://example.com/style.png" {
		t.Fatalf("second Grok image was not an ImageUrl object: %#v", images[1])
	}
	if _, ok := received["image"]; ok {
		t.Fatalf("single-image field leaked into Grok payload: %#v", received)
	}
	if _, ok := received["images"]; ok {
		t.Fatalf("legacy images field leaked into Grok payload: %#v", received)
	}
}

func TestWaninterGrok15ProviderUsesSingleImageURLString(t *testing.T) {
	payload := map[string]any{
		"model":    "grok-imagine-video-1.5-preview",
		"image":    map[string]any{"url": "https://example.com/source.png"},
		"duration": 6,
		"ratio":    "9:16",
	}
	normalizeWaninterVideoPayload(payload)

	if payload["seconds"] != "6" || payload["aspect_ratio"] != "9:16" {
		t.Fatalf("Grok 1.5 legacy options were not normalized: %#v", payload)
	}
	if payload["image_url"] != "https://example.com/source.png" {
		t.Fatalf("Grok 1.5 compatibility image_url string was not retained: %#v", payload)
	}
	for _, field := range []string{"image", "images", "reference_images"} {
		if _, ok := payload[field]; ok {
			t.Fatalf("Grok 1.5 %s field leaked into the legacy payload: %#v", field, payload)
		}
	}
}

func TestWaninterGrok15ExplicitGenerationsPathUsesLegacyEndpoint(t *testing.T) {
	var received map[string]any
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/videos" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "grok-15-task", "status": "queued"})
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)
	srv := newServer(config{arkBaseURL: "https://ark.example.com/api/v3", defaultProvider: providerVolcengineArk, maxResponseBytes: defaultMaxResponseSize, allowedDownloadHosts: []string{".example.com"}})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)
	req, err := http.NewRequest(http.MethodPost, plugin.URL+"/plugin/v1/videos", strings.NewReader(`{"model":"grok-imagine-video-1.5-preview","prompt":"animate","image":{"url":"https://example.com/source.png"},"duration":6,"ratio":"9:16"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer relay-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProvider, providerWaninter)
	req.Header.Set(headerProviderBaseURL, relayServer.URL)
	req.Header.Set(headerCreatePath, "/v1/videos/generations")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, data)
	}
	if received["seconds"] != "6" || received["aspect_ratio"] != "9:16" {
		t.Fatalf("Grok 1.5 successful legacy options were not restored: %#v", received)
	}
	if received["image_url"] != "https://example.com/source.png" {
		t.Fatalf("Grok 1.5 compatibility image_url string was not sent: %#v", received)
	}
	for _, field := range []string{"image", "images", "reference_images"} {
		if _, ok := received[field]; ok {
			t.Fatalf("Grok 1.5 %s field leaked upstream: %#v", field, received)
		}
	}
}

func TestCompatibleReferenceShapeDoesNotExposeURLs(t *testing.T) {
	payload := map[string]any{
		"image": "https://secret.example/source.png",
	}
	shape := compatibleReferenceShape(payload)
	if shape != "image=string" {
		t.Fatalf("unexpected reference shape: %s", shape)
	}
	if strings.Contains(shape, "secret.example") {
		t.Fatalf("reference shape leaked a URL: %s", shape)
	}
}

func TestWaninterSeedancePathBuildsContentPayload(t *testing.T) {
	var received map[string]any
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/contents/generations/tasks" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "seedance-task-1", "status": "queued"})
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)

	srv := newServer(config{
		arkBaseURL:           "https://ark.example.com/api/v3",
		defaultProvider:      providerVolcengineArk,
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".example.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)

	body := `{"model":"doubao-seedance-2-0-pro-250528","prompt":"camera move","images":["https://example.com/reference.png"],"duration":6,"ratio":"9:16","resolution":"720p","generate_audio":true,"watermark":false}`
	req, err := http.NewRequest(http.MethodPost, plugin.URL+"/plugin/v1/videos", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer relay-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProvider, providerWaninter)
	req.Header.Set(headerProviderBaseURL, relayServer.URL)
	req.Header.Set(headerCreatePath, "/v1/contents/generations/tasks")
	req.Header.Set(headerStatusPath, "/v1/contents/generations/tasks/{id}")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, data)
	}
	if _, ok := received["prompt"]; ok {
		t.Fatalf("seedance payload still contains prompt: %#v", received)
	}
	content, ok := received["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("unexpected seedance content: %#v", received["content"])
	}
	if content[0].(map[string]any)["text"] != "camera move" {
		t.Fatalf("prompt was not converted to text content: %#v", content[0])
	}
	image := content[1].(map[string]any)
	if image["role"] != "reference_image" {
		t.Fatalf("unexpected image role: %#v", image)
	}
	if image["image_url"].(map[string]any)["url"] != "https://example.com/reference.png" {
		t.Fatalf("image URL was not converted: %#v", image)
	}
}

func TestWaninterSeedanceModelUsesWaninterVideoEndpoint(t *testing.T) {
	var received map[string]any
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/videos" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "auto-seedance-task", "status": "queued"})
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)

	srv := newServer(config{
		arkBaseURL:           "https://ark.example.com/api/v3",
		defaultProvider:      providerVolcengineArk,
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".example.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)

	req, err := http.NewRequest(http.MethodPost, plugin.URL+"/plugin/v1/videos", strings.NewReader(`{"model":"seedance2.0","prompt":"a test","duration":16,"ratio":"16:9","reference_images":[{"url":"https://example.com/reference.png"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer relay-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProvider, providerWaninter)
	req.Header.Set(headerProviderBaseURL, relayServer.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, data)
	}
	if received["prompt"] != "a test" || received["seconds"] != "16" || received["aspect_ratio"] != "16:9" {
		t.Fatalf("Waninter payload was not preserved and normalized: %#v", received)
	}
	images, ok := received["images"].([]any)
	if !ok || len(images) != 1 || images[0] != "https://example.com/reference.png" {
		t.Fatalf("reference images were not normalized: %#v", received)
	}
	if _, ok := received["content"]; ok {
		t.Fatalf("Waninter OpenAI-video model was incorrectly converted to a content task: %#v", received)
	}
}

func TestWaninterKlingV3UsesNativeKlingEndpoint(t *testing.T) {
	var received map[string]any
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/kling/v1/videos/text2video" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"task_id": "kling-v3-task"}})
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)

	srv := newServer(config{
		arkBaseURL:           "https://ark.example.com/api/v3",
		defaultProvider:      providerVolcengineArk,
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".example.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)

	req, err := http.NewRequest(http.MethodPost, plugin.URL+"/plugin/v1/videos", strings.NewReader(`{"model":"kling-video-v3","prompt":"a test","duration":6,"ratio":"16:9"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer relay-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProvider, providerWaninter)
	req.Header.Set(headerProviderBaseURL, relayServer.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, data)
	}
	if received["model"] != "kling-video-v3" || received["model_name"] != "kling-video-v3" || received["duration"] != "6" || received["aspect_ratio"] != "16:9" {
		t.Fatalf("Kling payload was not converted: %#v", received)
	}
	if _, ok := received["seconds"]; ok {
		t.Fatalf("Kling payload still contains seconds: %#v", received)
	}
}

func TestWaninterKlingTaskStatusAndContent(t *testing.T) {
	requests := make([]string, 0, 1)
	relay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if r.URL.Path != "/kling/v1/videos/text2video/kling-v3-task" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"data": map[string]any{
				"task_id":     "kling-v3-task",
				"task_status": "succeed",
				"task_result": map[string]any{"videos": []any{map[string]any{"url": "https://example.com/kling.mp4"}}},
			},
		})
	})
	relayServer := httptest.NewServer(relay)
	t.Cleanup(relayServer.Close)

	srv := newServer(config{
		arkBaseURL:           "https://ark.example.com/api/v3",
		defaultProvider:      providerVolcengineArk,
		maxResponseBytes:     defaultMaxResponseSize,
		allowedDownloadHosts: []string{".example.com"},
	})
	plugin := httptest.NewServer(srv.routes())
	t.Cleanup(plugin.Close)

	req, err := http.NewRequest(http.MethodGet, plugin.URL+"/plugin/v1/videos/kling-v3-task", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer relay-secret")
	req.Header.Set(headerProvider, providerWaninter)
	req.Header.Set(headerProviderBaseURL, relayServer.URL)
	provider, _ := srv.providerConfig(req)
	if err := srv.storeTaskRoute(req, provider, "kling-v3-task", seedanceTaskRoute{StatusPath: "/kling/v1/videos/text2video/{id}", ContentPath: "/kling/v1/videos/text2video/{id}", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"done"`) || !strings.Contains(string(body), "kling.mp4") {
		t.Fatalf("unexpected Kling status response: %d %s", resp.StatusCode, body)
	}
	if len(requests) != 1 {
		t.Fatalf("unexpected number of Kling requests: %v", requests)
	}
}

func TestFirstJSONStringSupportsArrayPath(t *testing.T) {
	got := firstJSONString([]byte(`{"data":{"task_result":{"videos":[{"url":"https://example.com/kling.mp4"}]}}}`), "data.task_result.videos.0.url")
	if got != "https://example.com/kling.mp4" {
		t.Fatalf("unexpected array path result: %s", got)
	}
}

func TestNormalizeCompatibleTaskIncludesGenericResultURL(t *testing.T) {
	body := []byte(`{"data":{"status":"SUCCESS","result_url":"https://example.com/video.mp4"}}`)
	result := normalizeCompatibleTask(body, "task-generic")

	if got := result["status"]; got != "done" {
		t.Fatalf("status = %v, want done", got)
	}
	video, ok := result["video"].(map[string]any)
	if !ok {
		t.Fatalf("video = %#v, want object", result["video"])
	}
	if got := video["url"]; got != "https://example.com/video.mp4" {
		t.Fatalf("video.url = %v, want generic result URL", got)
	}
}

func TestNormalizeCompatibleStatusSubmittedIsPending(t *testing.T) {
	if got := normalizeCompatibleStatus("submitted"); got != "pending" {
		t.Fatalf("status = %q, want pending", got)
	}
}

func TestNormalizeCompatibleTaskPreservesBillingMetadata(t *testing.T) {
	body := []byte(`{"data":{"result":{"status":"succeeded","duration":25,"resolution":"1080p","video":{"url":"https://example.com/video.mp4"},"usage":{"completion_tokens":2100,"total_tokens":2100}}}}`)
	result := normalizeCompatibleTask(body, "task-usage")
	video := result["video"].(map[string]any)
	if video["duration"] != float64(25) || result["resolution"] != "1080p" {
		t.Fatalf("missing output metadata: %#v", result)
	}
	usage, ok := result["usage"].(map[string]any)
	if !ok || usage["completion_tokens"] != float64(2100) {
		t.Fatalf("missing provider token usage: %#v", result["usage"])
	}
}

func TestWaninterNativeKlingRoutingOnlyTargetsV3(t *testing.T) {
	if !isWaninterNativeKlingModel("kling-video-v3") {
		t.Fatal("kling-video-v3 should use the native Kling route")
	}
	for _, model := range []string{"kling-video-v3-omni", "kling-video-v3-turbo", "seedance2.0", "grok-imagine-video"} {
		if isWaninterNativeKlingModel(model) {
			t.Fatalf("model %s must keep its existing route", model)
		}
	}
}
