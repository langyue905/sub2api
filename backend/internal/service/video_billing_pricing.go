package service

import "context"

// VideoBillingPricing freezes the unit and unit price of an accepted async task.
// It is internal billing metadata, never part of the provider request.
type VideoBillingPricing struct {
	Mode      BillingMode `json:"mode"`
	UnitPrice float64     `json:"unit_price"`
}

func (p *VideoBillingPricing) Valid() bool {
	return p != nil && (p.Mode == BillingModeVideo || p.Mode == BillingModePerRequest) && validCost(p.UnitPrice)
}

func (p *VideoBillingPricing) cost(model string, count, seconds int, multiplier float64) *CostBreakdown {
	if !p.Valid() {
		return &CostBreakdown{}
	}
	if count <= 0 {
		count = 1
	}
	units := count
	if p.Mode == BillingModeVideo {
		units *= NormalizeVideoBillingDurationForModel(model, seconds)
	}
	if multiplier < 0 {
		multiplier = 0
	}
	total := p.UnitPrice * float64(units)
	return &CostBreakdown{TotalCost: total, ActualCost: total * multiplier, BillingMode: string(p.Mode)}
}

// Video resolutions are labels, not token context ranges. A missing tier may
// use an explicitly configured default, but must never select another tier.
func videoPerRequestPrice(resolved *ResolvedPricing, resolution string) float64 {
	if resolved == nil || resolved.Mode != BillingModePerRequest {
		return 0
	}
	for _, tier := range resolved.RequestTiers {
		if tier.TierLabel == resolution && tier.PerRequestPrice != nil {
			return *tier.PerRequestPrice
		}
	}
	return resolved.DefaultPerRequestPrice
}

// CaptureVideoBillingPricing is called before sending the create upstream.
// Legacy/token-priced tasks keep their existing settlement path.
func (s *OpenAIGatewayService) CaptureVideoBillingPricing(ctx context.Context, apiKey *APIKey, model, resolution string, seconds int) *VideoBillingPricing {
	if s == nil || s.billingService == nil {
		return nil
	}
	if resolved := s.resolveOpenAIChannelPricing(ctx, model, apiKey); resolved != nil && (resolved.Mode == BillingModeToken || resolved.Mode == BillingModeImage) {
		return nil
	}
	seconds = NormalizeVideoBillingDurationForModel(model, seconds)
	cost := s.calculateOpenAIVideoCost(ctx, model, apiKey, &OpenAIForwardResult{
		VideoCount: 1, VideoResolution: resolution, VideoDurationSeconds: seconds,
	}, 1)
	if cost == nil || !validCost(cost.TotalCost) {
		return nil
	}
	price := &VideoBillingPricing{Mode: BillingMode(cost.BillingMode), UnitPrice: cost.TotalCost}
	if price.Mode == BillingModeVideo {
		price.UnitPrice /= float64(seconds)
	}
	if !price.Valid() {
		return nil
	}
	return price
}
