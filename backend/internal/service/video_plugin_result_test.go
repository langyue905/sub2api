package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeVideoPluginPublicStatusSupportsNestedRelayResult(t *testing.T) {
	body := []byte(`{"data":{"result":{"status":"succeeded","video":{"url":"https://cdn.example.com/video.mp4"}}}}`)
	normalized, err := normalizeVideoPluginPublicStatus(body)
	require.NoError(t, err)
	require.Equal(t, "completed", gjson.GetBytes(normalized, "status").String())
	require.Equal(t, "https://cdn.example.com/video.mp4", gjson.GetBytes(normalized, "url").String())
}

func TestVideoPluginMappedModelUsesRequestedPrice(t *testing.T) {
	result := videoPluginForwardResult(grokMediaUsageMetadata{Model: "provider-model", BillingModel: "provider-model"}, "public-model", "provider-model", http.Header{}, time.Now())
	require.Equal(t, "public-model", result.Model)
	require.Equal(t, "public-model", result.BillingModel)
	require.Equal(t, "provider-model", result.UpstreamModel)
}

func TestVideoPluginStatusPreservesDurationWithoutProviderModel(t *testing.T) {
	meta := videoPluginUsageFromResponse(GrokMediaEndpointVideoStatus, GrokMediaRequestInfo{}, []byte(`{"id":"task","status":"done","video":{"url":"https://example.com/video.mp4","duration":25},"usage":{"completion_tokens":2100,"total_tokens":2100}}`))
	require.Equal(t, 25, meta.VideoDurationSeconds)
	require.Equal(t, 1, meta.VideoCount)
	require.Equal(t, 2100, meta.Usage.OutputTokens)
}

func TestVideoPluginRequiresExplicitPriceAndBillsFullDuration(t *testing.T) {
	s := &OpenAIGatewayService{}
	key := &APIKey{Group: &Group{}}
	require.ErrorIs(t, s.ValidateVideoPluginPricing(context.Background(), key, "sora-2", "720p"), ErrModelPricingUnavailable)
	key.Group.VideoModelPrices = map[string]map[string]float64{"sora-2": {"720p": 0.12}}
	require.NoError(t, s.ValidateVideoPluginPricing(context.Background(), key, "sora-2", "720p"))
	info := ParseGrokMediaRequest("application/json", []byte(`{"model":"sora-2","prompt":"test","seconds":25,"resolution":"720p"}`))
	require.Equal(t, 25, info.DurationSeconds)
	billing := &BillingService{}
	cost := billing.CalculateVideoCost("sora-2", "720p", 1, info.DurationSeconds, &VideoPriceConfig{ModelPrices: key.Group.VideoModelPrices}, 1)
	require.InDelta(t, 3, cost.TotalCost, 1e-9)
}
