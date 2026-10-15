package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestModelPlazaProfiles_RoundTrip(t *testing.T) {
	raw, err := encodeModelPlazaProfiles([]ModelPlazaProfile{{
		Model: " seedance-2.0 ", Summary: " 视频介绍 ", Description: " **详情** ",
		Tags: []string{" 视频 ", ""}, Specs: []ModelPlazaProfileSpec{{Label: " 比例 ", Value: " 16:9 "}, {}},
	}})
	require.NoError(t, err)
	profiles := parseModelPlazaProfiles(raw)
	require.Len(t, profiles, 1)
	require.Equal(t, "seedance-2.0", profiles[0].Model)
	require.Equal(t, []string{"视频"}, profiles[0].Tags)
	require.Equal(t, []ModelPlazaProfileSpec{{Label: "比例", Value: "16:9"}}, profiles[0].Specs)
	for _, empty := range []string{"", "null", "invalid"} {
		require.Empty(t, parseModelPlazaProfiles(empty))
	}
	raw, err = encodeModelPlazaProfiles(nil)
	require.NoError(t, err)
	require.Equal(t, "[]", raw)
}

func TestModelPlazaProfiles_RejectInvalidConfiguration(t *testing.T) {
	for name, profiles := range map[string][]ModelPlazaProfile{
		"missing name":        {{Summary: "missing model"}},
		"duplicate":           {{Model: "same"}, {Model: " same "}},
		"unsafe cover":        {{Model: "m", CoverURL: "javascript:alert(1)"}},
		"authenticated cover": {{Model: "m", CoverURL: "https://user:pass@example.com/a.png"}},
		"incomplete spec":     {{Model: "m", Specs: []ModelPlazaProfileSpec{{Label: "时长"}}}},
		"too many tags":       {{Model: "m", Tags: make([]string, 13)}},
		"too long summary":    {{Model: "m", Summary: strings.Repeat("x", 1501)}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := encodeModelPlazaProfiles(profiles)
			require.Error(t, err)
		})
	}
	_, err := encodeModelPlazaProfiles([]ModelPlazaProfile{{Model: "same"}, {Model: "same", Platform: "openai"}})
	require.NoError(t, err, "a platform-specific override is valid")
}

func TestModelPlazaProfiles_SettingsAndRuntime(t *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{values: map[string]string{SettingKeyModelPlazaEnabled: "true"}}
	svc := NewSettingService(repo, &config.Config{})
	settings, err := svc.GetAllSettings(ctx)
	require.NoError(t, err)
	settings.ModelPlazaProfiles = []ModelPlazaProfile{{Model: "test", Summary: "Test summary"}}
	require.NoError(t, svc.UpdateSettings(ctx, settings))
	saved, err := svc.GetAllSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "Test summary", saved.ModelPlazaProfiles[0].Summary)
	runtime := svc.GetModelPlazaRuntime(ctx)
	require.True(t, runtime.Enabled)
	require.Equal(t, saved.ModelPlazaProfiles, runtime.Profiles)
}
