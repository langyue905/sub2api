package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskRoutesSurviveRestartAndIsolateAccounts(t *testing.T) {
	cfg := config{taskRouteDir: t.TempDir()}
	provider := providerConfig{protocol: providerWaninter, baseURL: "https://relay.example.com", statusPath: defaultStatusPath, contentPath: defaultContentPath}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer upstream-secret")
	req.Header.Set("X-Sub2-Video-Account-ID", "42")
	srv := newServer(cfg)
	route := seedanceTaskRoute{StatusPath: waninterKlingStatusPath, ContentPath: waninterKlingStatusPath, CreatedAt: time.Now()}
	if err := srv.storeTaskRoute(req, provider, "same-task", route); err != nil {
		t.Fatal(err)
	}
	// A new process and a rotated credential still use the original account's route.
	srv = newServer(cfg)
	req.Header.Set("Authorization", "Bearer rotated-key")
	if status, _ := srv.videoTaskPaths(req, provider, "same-task"); status != waninterKlingStatusPath {
		t.Fatalf("route lost across restart: %s", status)
	}
	req.Header.Set("X-Sub2-Video-Account-ID", "43")
	if status, _ := srv.videoTaskPaths(req, provider, "same-task"); status != defaultStatusPath {
		t.Fatal("another account inherited task route")
	}
	req.Header.Set("X-Sub2-Video-Account-ID", "42")
	provider.baseURL = "https://other.example.com"
	if status, _ := srv.videoTaskPaths(req, provider, "same-task"); status != defaultStatusPath {
		t.Fatal("another supplier inherited task route")
	}
	entries, _ := os.ReadDir(cfg.taskRouteDir)
	for _, entry := range entries {
		data, _ := os.ReadFile(filepath.Join(cfg.taskRouteDir, entry.Name()))
		if strings.Contains(string(data), "secret") || strings.Contains(string(data), "rotated-key") {
			t.Fatal("persisted upstream credential")
		}
	}
}
