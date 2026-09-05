# Image Playground source

- Repository: https://github.com/CookSleep/gpt_image_playground
- Commit: f08385c (v0.7.8)
- Build command: `VITE_DEFAULT_API_URL=https://api.zshai.cc/v1 npm run build`
- API base URL in the generated bundle: `https://api.zshai.cc/v1`
- Sub2API local patch: `fix1` repairs missing IndexedDB originals and prevents image IDs from being fetched as URLs during download.
- Sub2API local patch: `fix3` parses `data:` image URLs into `Blob` objects locally, so image edits do not depend on browser `fetch(data:)` support; the embedded build also retains the `fix1` original-image recovery behavior.
