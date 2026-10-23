// Local browser regression. Requires a built canvas and Playwright; no provider calls.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../dist');
const prefix = '/playgrounds/canvas/';
const mime = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' };
const server = http.createServer((req, res) => {
    const pathname = decodeURIComponent(new URL(req.url, 'http://localhost').pathname);
    let file = path.resolve(root, '.' + '/' + pathname.slice(prefix.length));
    if (!file.startsWith(root + path.sep) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) file = path.join(root, 'index.html');
    res.writeHead(200, { 'Content-Type': mime[path.extname(file)] || 'application/octet-stream' });
    fs.createReadStream(file).pipe(res);
});
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function until(check, label) {
    for (let i = 0; i < 120; i++) { if (await check()) return; await wait(100); }
    throw new Error('Timed out: ' + label);
}
async function run(browser, base, width) {
    const ctx = await browser.newContext({ viewport: { width, height: 950 }, locale: 'zh-CN' });
    const config = { apiKey: 'mock-only', videoModel: 'zshai::MiniMax-H3', videoMode: 'reference', vquality: '768', size: '16:9', videoSeconds: '4', channels: [{ id: 'zshai', models: [{ name: 'MiniMax-H3', capability: 'video' }] }] };
    await ctx.addInitScript((config) => {
        if (!localStorage.getItem('infinite-canvas:ai_config_store')) localStorage.setItem('infinite-canvas:ai_config_store', JSON.stringify({ state: { config }, version: 0 }));
        localStorage.setItem('infinite-canvas:locale', 'zh-CN');
    }, config);
    const page = await ctx.newPage(), errors = [], posts = [], queries = [], states = new Map();
    let releaseFirst;
    const firstGate = new Promise((resolve) => { releaseFirst = resolve; });
    page.on('pageerror', (error) => errors.push(error.message));
    await page.route('**/*', async (route) => {
        const req = route.request(), url = new URL(req.url());
        if (url.pathname === '/v1/videos/generations' && req.method() === 'POST') {
            const body = req.postDataJSON(); posts.push(body);
            if (body.prompt === 'uncertain') return route.fulfill({ status: 503, json: { error: 'mock uncertain' } });
            const id = `task-${posts.length}`;
            states.set(id, { status: 'in_progress', progress: `${posts.length * 10}%` });
            if (posts.length === 1) await firstGate;
            return route.fulfill({ json: { id, status: 'queued' } });
        }
        if (url.pathname.startsWith('/v1/videos/')) {
            if (url.pathname.endsWith('/content')) return route.fulfill({ status: 503, body: 'Mock content unavailable' });
            const id = url.pathname.split('/').pop(); queries.push(id);
            return route.fulfill({ json: { id, ...states.get(id) } });
        }
        if (url.origin !== base) return route.abort();
        if (url.pathname.startsWith('/api/')) return route.fulfill({ json: { code: 0, data: { items: [], total: 0 } } });
        return route.continue();
    });
    try {
        await page.goto(base + prefix + '#/video');
        const generate = page.locator('button').filter({ hasText: '开始生成' });
        const prompt = page.locator('textarea').first();
        await prompt.fill('first independent task');
        // Two synchronous clicks must not create two POSTs while submission is pending.
        await generate.evaluate((button) => { button.click(); button.click(); });
        await until(() => posts.length === 1, 'first POST');
        assert(await generate.isDisabled());
        releaseFirst();
        await until(() => queries.includes('task-1'), 'first query');
        await until(() => generate.isEnabled(), 'unlock after task ID');
        await prompt.fill('second independent task');
        await generate.click();
        await until(() => queries.includes('task-2'), 'second query');
        const cards = page.locator('[data-video-task]');
        assert.equal(await cards.count(), 2);
        const first = cards.filter({ hasText: 'first indepe' }), second = cards.filter({ hasText: 'second indep' });
        await first.getByText('10%', { exact: true }).waitFor();
        await second.getByText('20%', { exact: true }).waitFor();
        if (process.env.QA_OUTPUT) await page.screenshot({ path: path.join(process.env.QA_OUTPUT, `concurrency-pending-${width}.png`) });
        const firstId = await first.getAttribute('data-video-task');
        const secondId = await second.getAttribute('data-video-task');
        async function openLogs() {
            if (width < 1024) await page.getByRole('button', { name: /历史|记录/ }).first().click();
        }
        await openLogs();
        await page.getByRole('button').filter({ hasText: 'first indepe' }).first().click();
        states.set('task-2', { status: 'in_progress', progress: '77%' });
        const before = queries.filter((id) => id === 'task-2').length;
        await until(() => queries.filter((id) => id === 'task-2').length > before, 'background update');
        assert.equal(await cards.count(), 1);
        assert.equal(await cards.first().getAttribute('data-video-task'), firstId, 'background must not change selected history');
        await page.getByRole('button', { name: '查看全部任务', exact: true }).click();
        await second.getByText('77%', { exact: true }).waitFor();
        await first.getByRole('button', { name: '停止等待', exact: true }).click();
        await until(async () => await first.getAttribute('data-status') === 'stopped', 'first stopped');
        const stoppedQueries = queries.filter((id) => id === 'task-1').length;
        const otherQueries = queries.filter((id) => id === 'task-2').length;
        await until(() => queries.filter((id) => id === 'task-2').length > otherQueries, 'other task continues');
        assert.equal(queries.filter((id) => id === 'task-1').length, stoppedQueries);
        await first.getByRole('button', { name: '继续查询', exact: true }).click();
        await until(() => queries.filter((id) => id === 'task-1').length > stoppedQueries, 'resume original ID');
        await openLogs();
        await page.getByRole('button', { name: '新建', exact: true }).click();
        if (width < 1024) await page.locator('.ant-drawer-close').click();
        assert.equal(await prompt.inputValue(), '');
        assert.equal(await cards.count(), 2, 'new form preserves background tasks');
        await page.reload();
        await until(async () => await cards.count() === 2, 'restore both pending tasks');
        assert.equal(posts.length, 2, 'reload and resume never POST');
        assert.deepEqual((await cards.evaluateAll((els) => els.map((el) => el.dataset.videoTask))).sort(), [firstId, secondId].sort());
        states.set('task-2', { status: 'completed', result_url: 'https://media.example.invalid/second.mp4' });
        await until(async () => await page.locator(`[data-video-task="${secondId}"]`).getAttribute('data-status') === 'success', 'second success');
        assert.equal(await page.locator(`[data-video-task="${firstId}"]`).getAttribute('data-status'), 'pending');
        states.set('task-1', { status: 'completed', result_url: 'https://media.example.invalid/first.mp4' });
        await until(async () => await page.locator(`[data-video-task="${firstId}"]`).getAttribute('data-status') === 'success', 'first success');
        assert.equal(await page.locator(`[data-video-task="${firstId}"] video`).getAttribute('src'), 'https://media.example.invalid/first.mp4');
        assert.equal(await page.locator(`[data-video-task="${secondId}"] video`).getAttribute('src'), 'https://media.example.invalid/second.mp4');
        if (process.env.QA_OUTPUT) await page.screenshot({ path: path.join(process.env.QA_OUTPUT, `concurrency-success-${width}.png`) });
        await prompt.fill('uncertain'); await generate.click();
        await until(async () => await page.locator('[data-status="interrupted"]').count() === 1, 'uncertain submission retained');
        await page.reload(); await prompt.waitFor();
        assert.equal(posts.length, 3, 'uncertain submission never auto retries');
        assert.deepEqual(errors, []);
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
        if (process.env.QA_OUTPUT) await page.screenshot({ path: path.join(process.env.QA_OUTPUT, `concurrency-${width}.png`) });
        console.log(JSON.stringify({ width, posts: posts.length, doubleClickGuard: true, simultaneousTasks: 2, independentStop: true, historyIsolation: true, reloadNoPost: true, resultsCorrect: true, uncertainNoRetry: true, pageErrors: 0 }));
    } finally { releaseFirst(); await ctx.close(); }
}
(async () => {
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    let browser;
    try {
        browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_EXECUTABLE ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE } : {}) });
        for (const width of [1600, 390]) await run(browser, `http://127.0.0.1:${server.address().port}`, width);
    } finally { if (browser) await browser.close(); server.close(); }
})().catch((error) => { console.error(error); process.exitCode = 1; });
