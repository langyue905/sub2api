//go:build unit

package handler

import (
	"context"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMiniMaxH3CompletionRequiresValidSnapshotBeforeClaim(t *testing.T) {
	for _, alias := range []bool{false, true} {
		h, _, bindings, _ := newGrokMediaSlotHandler(t, false, false, service.PlatformVideoPlugin)
		c, _ := grokMediaSlotContext(context.Background(), false)
		key, _ := middleware2.GetAPIKeyFromContext(c)
		model := "MiniMax-H3"
		if alias {
			model = "public-h3"
		}
		key.Group.VideoModelPrices = map[string]map[string]float64{model: {"2k": .25}}
		if !alias {
			key.Group.VideoModelPrices = map[string]map[string]float64{"minimax-h3": {"2k": .25}}
		}
		subject := middleware2.AuthSubject{UserID: 10, Concurrency: 5}
		result := &service.OpenAIForwardResult{ResponseID: "h3-task", Model: model, UpstreamModel: "MiniMax-H3", VideoCount: 1, VideoDurationSeconds: 99}
		prepare := func() *service.OpenAIForwardResult {
			return prepareGrokVideoCompletionBilling(c.Request.Context(), h, zap.NewNop(), key, subject, "h3-task", result)
		}
		require.Nil(t, prepare())
		require.Empty(t, bindings.billed)
		pending := service.GrokVideoPendingBilling{Model: model, BillingModel: model, UpstreamModel: "MiniMax-H3", VideoDurationSeconds: 5, VideoResolution: "480p"}
		store := func() {
			require.NoError(t, h.gatewayService.StoreGrokVideoPendingBilling(c.Request.Context(), "h3-task", 10, 20, pending))
		}
		store()
		require.Nil(t, prepare())
		require.Empty(t, bindings.billed)
		pending.VideoResolution = "2k"
		pending.VideoDurationSeconds = 3
		store()
		require.Nil(t, prepare())
		require.Empty(t, bindings.billed)
		pending.VideoDurationSeconds = 5
		key.Group.VideoModelPrices = nil
		store()
		require.Nil(t, prepare())
		require.Empty(t, bindings.billed)
		key.Group.VideoModelPrices = map[string]map[string]float64{"minimax-h3": {"2k": .25}, "public-h3": {"2k": .25}}
		bill := prepare()
		require.NotNil(t, bill)
		require.Equal(t, "2k", bill.VideoResolution)
		require.Equal(t, 5, bill.VideoDurationSeconds)
		require.Equal(t, model, bill.BillingModel)
		for range 10 {
			require.Nil(t, prepare())
		}
		require.Len(t, bindings.billed, 1)
	}
}
