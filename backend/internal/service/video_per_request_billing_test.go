//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func videoRequestPricingService(t *testing.T, source string, mode BillingMode, price float64) (*OpenAIGatewayService, *APIKey) {
	t.Helper()
	const groupID = int64(25)
	billing := NewBillingService(&config.Config{}, nil)
	entry := ChannelModelPricing{Models: []string{"MiniMax-H3"}, BillingMode: mode, PerRequestPrice: &price}
	key := &APIKey{ID: 20, User: &User{ID: 10}, GroupID: i64p(groupID), Group: &Group{ID: groupID, Platform: PlatformVideoPlugin, RateMultiplier: 1}}
	cs := &ChannelService{}
	cache := newEmptyChannelCache()
	cache.loadedAt = time.Now()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformVideoPlugin, model: normalizeChannelPricingModelName("MiniMax-H3")}] = &entry
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformVideoPlugin
	cs.cache.Store(cache)
	if source == PricingSourceGroup {
		key.Group.ModelPricing = []ChannelModelPricing{entry}
	}
	return &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(cs, billing)}, key
}

func TestVideoPerRequestPricingDoesNotMultiplyDuration(t *testing.T) {
	for _, source := range []string{PricingSourceGroup, PricingSourceChannel} {
		for _, mode := range []BillingMode{BillingModePerRequest, BillingModeVideo} {
			t.Run(source+"/"+string(mode), func(t *testing.T) {
				svc, key := videoRequestPricingService(t, source, mode, .25)
				if mode == BillingModeVideo {
					// H3 per-second prices still require an exact resolution tier.
					resolved := svc.resolveOpenAIChannelPricing(context.Background(), "MiniMax-H3", key)
					require.NotNil(t, resolved)
					intervals := []PricingInterval{{TierLabel: "2k", PerRequestPrice: resolved.channelPricing.PerRequestPrice}}
					resolved.channelPricing.Intervals = intervals
					if source == PricingSourceGroup {
						key.Group.ModelPricing[0].Intervals = intervals
					} else {
						cache := svc.resolver.channelService.cache.Load().(*channelCache)
						cache.pricingByGroupModel[channelModelKey{groupID: key.Group.ID, platform: PlatformVideoPlugin, model: normalizeChannelPricingModelName("MiniMax-H3")}].Intervals = intervals
					}
				}
				for _, seconds := range []int{4, 15} {
					require.NoError(t, svc.ValidateVideoPluginPricing(context.Background(), key, "MiniMax-H3", "2k"))
					want := .25
					if mode == BillingModeVideo {
						want *= float64(seconds)
					}
					cost := svc.calculateOpenAIVideoCost(context.Background(), "MiniMax-H3", key, &OpenAIForwardResult{
						VideoCount: 1, VideoResolution: "2k", VideoDurationSeconds: seconds,
					}, 1.7)
					require.InDelta(t, want, cost.TotalCost, 1e-12)
					require.InDelta(t, want*1.7, cost.ActualCost, 1e-12)
					require.Equal(t, string(mode), cost.BillingMode)
				}
			})
		}
	}
}

func TestVideoPerRequestGroupOverrideAndTierValidation(t *testing.T) {
	svc, key := videoRequestPricingService(t, PricingSourceGroup, BillingModePerRequest, .5)
	key.Group.VideoModelPrices = map[string]map[string]float64{"minimax-h3": {"2k": 9}}
	p := 1.25
	key.Group.ModelPricing[0].Intervals = []PricingInterval{{TierLabel: "2k", PerRequestPrice: &p}}
	for _, model := range []string{"MiniMax-H3", "public-video"} {
		key.Group.ModelPricing[0].Models = []string{model}
		for _, tier := range []string{"2k", "4k"} {
			require.NoError(t, svc.ValidateVideoPluginPricing(context.Background(), key, model, tier, "MiniMax-H3"))
		}
	}
	key.Group.ModelPricing[0].Models = []string{"MiniMax-H3"}
	cost := svc.calculateOpenAIVideoCost(context.Background(), "MiniMax-H3", key, &OpenAIForwardResult{VideoCount: 1, VideoResolution: "2k", VideoDurationSeconds: 15}, 1)
	require.Equal(t, 1.25, cost.TotalCost, "explicit group per-request card overrides legacy per-second prices")
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		key.Group.ModelPricing[0].PerRequestPrice = &bad
		key.Group.ModelPricing[0].Intervals[0].PerRequestPrice = &bad
		require.Error(t, svc.ValidateVideoPluginPricing(context.Background(), key, "MiniMax-H3", "2k"))
		require.Error(t, svc.ValidateVideoPluginPricing(context.Background(), key, "MiniMax-H3", "4k"))
	}
	// A price for 2k must not accidentally pass for an unpriced 4k request.
	key.Group.ModelPricing[0].PerRequestPrice = nil
	key.Group.ModelPricing[0].Intervals[0].PerRequestPrice = &p
	require.Error(t, svc.ValidateVideoPluginPricing(context.Background(), key, "MiniMax-H3", "4k"))
	require.Error(t, svc.ValidateVideoPluginPricing(context.Background(), key, "MiniMax-H3", "480p"))
	key.Group.ModelPricing[0].Models = []string{"custom-video"}
	require.Error(t, svc.ValidateVideoPluginPricing(context.Background(), key, "custom-video", "4k"))
}

func TestVideoPerRequestSnapshotSurvivesSerializationAndModeChange(t *testing.T) {
	svc, key := videoRequestPricingService(t, PricingSourceGroup, BillingModePerRequest, .5)
	pending := GrokVideoPendingBilling{Model: "MiniMax-H3", BillingModel: "MiniMax-H3", VideoResolution: "2k", VideoDurationSeconds: 15,
		Pricing: svc.CaptureVideoBillingPricing(context.Background(), key, "MiniMax-H3", "2k", 15)}
	require.NotNil(t, pending.Pricing)
	data, err := json.Marshal(pending)
	require.NoError(t, err)
	var restored GrokVideoPendingBilling
	require.NoError(t, json.Unmarshal(data, &restored))
	// A task keeps its accepted per-request price even if the current card becomes token-priced.
	key.Group.ModelPricing[0].BillingMode = BillingModeToken
	result := &OpenAIForwardResult{VideoCount: 1, VideoResolution: "2k", VideoDurationSeconds: 15, VideoBillingPricing: restored.Pricing}
	cost, err := svc.calculateOpenAIRecordUsageCost(context.Background(), result, key, []string{"MiniMax-H3"}, 1, 1, 2, 1, UsageTokens{}, "", nil, false, time.Now())
	require.NoError(t, err)
	require.Equal(t, .5, cost.TotalCost)
	require.Equal(t, 1.0, cost.ActualCost)
	require.Equal(t, string(BillingModePerRequest), cost.BillingMode)
	// Old task JSON remains readable and follows the original pricing path.
	require.NoError(t, json.Unmarshal([]byte(`{"model":"old-video","video_duration_seconds":5}`), &restored))
	var legacy GrokVideoPendingBilling
	require.NoError(t, json.Unmarshal([]byte(`{"model":"old-video","video_duration_seconds":5}`), &legacy))
	require.Nil(t, legacy.Pricing)
}

func TestVideoPerRequestInflightUsesSettlementUnitsAndVideoRate(t *testing.T) {
	for _, source := range []string{PricingSourceGroup, PricingSourceChannel} {
		for _, mode := range []BillingMode{BillingModePerRequest, BillingModeVideo} {
			svc, key := videoRequestPricingService(t, source, mode, .25)
			key.Group.VideoRateIndependent, key.Group.VideoRateMultiplier = true, .5
			d := inflightEstimateDeps{resolver: svc.resolver, billing: svc.billingService}
			for _, seconds := range []int{4, 15} {
				t.Run(fmt.Sprintf("%s/%s/%d", source, mode, seconds), func(t *testing.T) {
					want := .25 * .5
					if mode == BillingModeVideo {
						want *= float64(seconds)
					}
					estimate := d.estimateOne(context.Background(), key, "MiniMax-H3", InflightEstimateRequest{Kind: InflightEstimateVideo, Units: 1, VideoResolution: "2k", VideoDurationSeconds: seconds}, 3, 9)
					require.InDelta(t, want, estimate, 1e-12, "must not reserve image rate or per-second fallback for per-request video")
				})
			}
		}
	}
}

func TestVideoPerRequestUsageLogsAndDeductsOneVideo(t *testing.T) {
	for _, seconds := range []int{4, 15} {
		pricing, key := videoRequestPricingService(t, PricingSourceGroup, BillingModePerRequest, .5)
		key.Group.VideoRateIndependent, key.Group.VideoRateMultiplier = true, .4
		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		userRepo := &openAIRecordUsageUserRepoStub{}
		svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
		svc.resolver = pricing.resolver
		require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			APIKey: key, User: key.User, Account: &Account{ID: 1, Platform: PlatformVideoPlugin},
			Result: &OpenAIForwardResult{ResponseID: fmt.Sprintf("video-%d", seconds), Model: "MiniMax-H3", BillingModel: "MiniMax-H3", VideoCount: 1, VideoResolution: "2k", VideoDurationSeconds: seconds},
		}))
		require.Equal(t, .5, usageRepo.lastLog.TotalCost)
		require.InDelta(t, .2, usageRepo.lastLog.ActualCost, 1e-12)
		require.Equal(t, string(BillingModePerRequest), *usageRepo.lastLog.BillingMode)
		require.Equal(t, 1, usageRepo.lastLog.VideoCount)
		require.Zero(t, usageRepo.lastLog.ImageCount)
		require.Equal(t, seconds, *usageRepo.lastLog.VideoDurationSeconds)
		require.Equal(t, 1, userRepo.deductCalls)
		require.InDelta(t, .2, userRepo.lastAmount, 1e-12)
	}
}
