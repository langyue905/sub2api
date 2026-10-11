import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildApiUrl, FIXED_API_BASE_URL } from './devProxy'

afterEach(() => {
  vi.unstubAllEnvs()
})

describe('buildApiUrl', () => {
  it('uses the fixed platform API address for production builds', async () => {
    vi.stubEnv('PROD', true)
    vi.stubEnv('VITE_TEST_FIXED_API_BASE_URL', 'true')
    const { buildApiUrl, isApiBaseUrlLocked } = await import('./devProxy')

    expect(isApiBaseUrlLocked()).toBe(true)
    expect(buildApiUrl('https://attacker.example/v1', 'images/generations', null, false)).toBe(
      `${FIXED_API_BASE_URL}/images/generations`,
    )
  })

  it('uses the same-origin proxy prefix when API proxy is enabled', () => {
    expect(buildApiUrl('http://api.example.com/v1', 'images/edits', null, true)).toBe(
      '/api-proxy/images/edits',
    )
  })

  it('leaves API versioning to the proxy target when proxying', () => {
    expect(buildApiUrl('http://api.example.com', 'images/generations', null, true)).toBe(
      '/api-proxy/images/generations',
    )
  })

  it('uses a configured proxy prefix when one is available', () => {
    expect(
      buildApiUrl(
        'http://api.example.com/v1',
        'responses',
        {
          enabled: true,
          prefix: '/openai-proxy',
          target: 'http://api.example.com/v1',
          changeOrigin: true,
          secure: false,
        },
        true,
      ),
    ).toBe('/openai-proxy/responses')
  })

  it('uses the configured API URL directly when API proxy is disabled', () => {
    expect(buildApiUrl('http://api.example.com/v1', 'responses', null, false)).toBe(
      'http://api.example.com/v1/responses',
    )
  })

  it('locks requests to the same-origin New API relay when deployment lock is enabled', () => {
    vi.stubEnv('VITE_LOCK_API_BASE_URL', 'true')

    expect(buildApiUrl('https://attacker.example/v1', 'images/generations', null, false)).toBe(
      '/v1/images/generations',
    )
  })
})
