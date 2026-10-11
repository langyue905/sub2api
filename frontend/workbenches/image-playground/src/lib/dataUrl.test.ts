import { describe, expect, it } from 'vitest'
import { blobToDataUrl, bytesToDataUrl, dataUrlToBlob, dataUrlToBytes } from './dataUrl'

describe('dataUrl helpers', () => {
  it('converts image data URLs to bytes with extension', () => {
    const result = dataUrlToBytes('data:image/png;base64,AQID')

    expect(result.ext).toBe('png')
    expect(Array.from(result.bytes)).toEqual([1, 2, 3])
  })

  it('converts bytes to image data URLs from file extension', () => {
    expect(bytesToDataUrl(new Uint8Array([1, 2, 3]), 'images/output.webp')).toBe('data:image/webp;base64,AQID')
  })

  it('converts Base64 data URLs to blobs without using fetch', async () => {
    const originalFetch = globalThis.fetch
    globalThis.fetch = () => Promise.reject(new Error('blocked by CSP'))

    try {
      const blob = dataUrlToBlob('data:image/png;base64,AQID')

      expect(blob.type).toBe('image/png')
      expect(Array.from(new Uint8Array(await blob.arrayBuffer()))).toEqual([1, 2, 3])
    } finally {
      globalThis.fetch = originalFetch
    }
  })

  it('converts blobs to data URLs with fallback MIME', async () => {
    await expect(blobToDataUrl(new Blob([new Uint8Array([1, 2, 3])]), 'image/png')).resolves.toBe('data:image/png;base64,AQID')
  })
})
