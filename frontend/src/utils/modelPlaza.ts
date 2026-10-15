import type { ModelPlazaGroup, PlazaModel } from '@/api/modelPlaza'
import { BILLING_MODE_IMAGE, BILLING_MODE_VIDEO } from '@/constants/channel'
import { resolveIntervalPrices } from './pricing'

export function plazaModelRate(model: PlazaModel, group: Pick<ModelPlazaGroup,
  'rate_multiplier' | 'user_rate_multiplier' | 'image_rate_independent' | 'image_rate_multiplier' |
  'video_rate_independent' | 'video_rate_multiplier'>): number {
  if (model.pricing?.billing_mode === BILLING_MODE_VIDEO && group.video_rate_independent) {
    return Math.max(0, group.video_rate_multiplier ?? 1)
  }
  if (model.pricing?.billing_mode === BILLING_MODE_IMAGE && group.image_rate_independent) {
    return group.image_rate_multiplier ?? 1
  }
  return group.user_rate_multiplier ?? group.rate_multiplier
}

export function plazaUnitKey(model: PlazaModel): string {
  switch (model.pricing?.billing_mode) {
    case BILLING_MODE_VIDEO: return 'modelPlaza.table.perUnitSecond'
    case BILLING_MODE_IMAGE: return 'modelPlaza.table.perUnitImage'
    case 'per_request': return 'modelPlaza.table.perUnitRequest'
    default: return 'modelPlaza.cards.perMillion'
  }
}

/** The first context tier is the base-price preview; complete tiers remain in the details table. */
export function plazaBaseTokenPrices(model: PlazaModel) {
  const pricing = model.pricing
  if (!pricing) return null
  const first = [...(pricing.intervals ?? [])].sort((a, b) => a.min_tokens - b.min_tokens)[0]
  return first ? resolveIntervalPrices(first, pricing) : pricing
}
