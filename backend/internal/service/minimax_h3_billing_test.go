package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMiniMaxH3AliasCreatePricingAndSnapshot(t *testing.T) {
	account := &Account{Credentials: map[string]any{"model_mapping": map[string]any{"public-video": "MiniMax-H3"}}}
	svc := &OpenAIGatewayService{}
	key := &APIKey{Group: &Group{VideoModelPrices: map[string]map[string]float64{"public-video": {"2k": .25}}}}
	body, info, err := PrepareVideoPluginCreateRequest(account, "application/json", []byte(`{"model":"public-video","prompt":"test","resolution":"2k","aspect_ratio":"1:1","seconds":15}`))
	require.NoError(t, err)
	require.Equal(t, "public-video", info.Model)
	require.Equal(t, "public-video", gjson.GetBytes(body, "model").String())
	require.Equal(t, "2k", info.Resolution)
	require.Equal(t, 15, info.DurationSeconds)
	require.NoError(t, svc.ValidateVideoPluginPricing(context.Background(), key, info.Model, info.Resolution, account.GetMappedModel(info.Model)))
	key.Group.VideoModelPrices["public-video"]["2k"] = 0
	require.Error(t, svc.ValidateVideoPluginPricing(context.Background(), key, info.Model, info.Resolution, account.GetMappedModel(info.Model)))
	_, _, err = PrepareVideoPluginCreateRequest(account, "application/json", []byte(`{"model":"public-video","prompt":"test","resolution":"2160p"}`))
	require.Error(t, err)
}

func TestMiniMaxH3BillingFromNormalizedRequest(t *testing.T) {
	svc := &OpenAIGatewayService{billingService: &BillingService{}}
	prices := map[string]float64{"768p": .12, "1080p": .20, "2k": .25, "4k": .30}
	key := &APIKey{Group: &Group{VideoModelPrices: map[string]map[string]float64{"minimax-h3": prices}}}
	for tier, price := range prices {
		body, err := NormalizeMiniMaxH3Request("application/json", []byte(fmt.Sprintf(`{"model":"MiniMax-H3","prompt":"test","resolution":%q,"aspect_ratio":"1:1","seconds":15}`, tier)))
		require.NoError(t, err)
		info := ParseGrokMediaRequest("application/json", body)
		require.Equal(t, tier, info.Resolution)
		require.Equal(t, 15, info.DurationSeconds)
		require.NoError(t, svc.ValidateVideoPluginPricing(context.Background(), key, info.Model, info.Resolution))
		cost := svc.calculateOpenAIVideoCost(context.Background(), info.Model, key, &OpenAIForwardResult{VideoCount: 1, VideoResolution: info.Resolution, VideoDurationSeconds: info.DurationSeconds}, 1.7)
		require.InDelta(t, price*15, cost.TotalCost, 1e-9)
		require.InDelta(t, price*15*1.7, cost.ActualCost, 1e-9)
	}
	for _, price := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		key.Group.VideoModelPrices["minimax-h3"] = map[string]float64{"2k": price}
		require.Error(t, svc.ValidateVideoPluginPricing(context.Background(), key, "MiniMax-H3", "2k"))
	}
	key.Group.VideoModelPrices = nil
	for _, tier := range []string{"2k", "480p", "2160p", "4320p", "unknown", ""} {
		require.Error(t, svc.ValidateVideoPluginPricing(context.Background(), key, "MiniMax-H3", tier))
	}
	for _, label := range []string{"2K", "480p", ""} {
		p := .25
		require.False(t, h3HasExactVideoPrice(&ResolvedPricing{Mode: BillingModeVideo, RequestTiers: []PricingInterval{{TierLabel: label, PerRequestPrice: &p}}}, "2k"))
	}
}

func TestMiniMaxH3CreateNeverAutomaticallyRetries(t *testing.T) {
	for _, transport := range []bool{false, true} {
		t.Run(fmt.Sprint(transport), func(t *testing.T) {
			upstream := &videoPluginCreateUpstream{response: `{"error":{"message":"outcome unknown"}}`}
			if transport {
				upstream.err = errors.New("connection reset")
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 289, Platform: PlatformVideoPlugin, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "base_url": "http://video-plugin-test:8081", "provider_base_url": "https://vip.kkone.vip"}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "https://example.com/v1/videos", nil)
			_, err := svc.ForwardVideoPlugin(context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", []byte(`{"model":"MiniMax-H3","prompt":"test","resolution":"2k","aspect_ratio":"16:9"}`), "application/json")
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.False(t, failover.ShouldRetryNextAccount())
			require.False(t, failover.RetryableOnSameAccount)
			require.Equal(t, 1, upstream.calls)
		})
	}
}
