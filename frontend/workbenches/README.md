# 本站工作台

Sub2API 基线为 v0.2.15，保留本站图片工作台、邀请关系管理等既有修改。

- `infinite-canvas/`：基于 https://github.com/basketikun/infinite-canvas 的 v0.20.0（`3a6b64b602ad062b29fac66902ed18a7ef5fd9dc`）。整合朋友 20260915 源码包中的视频任务 ID、嵌套结果 URL、旧分辨率参数兼容与 Grok JSON 请求适配；通过 Sub2API 原生 Grok 视频路由调用，不部署朋友主站或本地代理插件。
- `image-playground/`：本站现有图片工作台完整可构建源码快照，原项目 https://github.com/CookSleep/gpt_image_playground 。原发布记录为 f08385c（v0.7.8），本目录另含本站后续修改；不以该上游版本号代表当前快照。保留图像编辑、下载和本地存储修复，并固定生产 API URL。

两个工作台的生产请求均使用 `https://api.zshai.cc/v1`。客户填写自己的 API Key，不能修改接口地址；不在仓库中存放客户密钥。画布的视频入口位于 `/canvas-workbench` 内，独立地址为 `/playgrounds/canvas/#/video`。实际可调用的视频模型取决于后台已配置的 Grok 渠道和用户密钥权限。

## 在本机重新构建

Node.js 20.19+，两个子项目使用各自的 npm lockfile：

```sh
npm --prefix frontend/workbenches/infinite-canvas ci
npm --prefix frontend/workbenches/image-playground ci
node frontend/workbenches/build.mjs
cd frontend
pnpm install --frozen-lockfile
pnpm build
cd ../backend
go build -tags embed -o bin/server ./cmd/server
```

`build.mjs` 先检查画布类型，再构建两个工作台并同步到 `frontend/public/playgrounds/`。保留上一版带哈希的资源文件，避免切换容器后已打开的旧页面无法加载延迟模块。主站构建再将静态资源嵌入后端；仅修改子项目源码而不运行同步命令不会更新线上页面。

视频请求使用 `/v1/videos/generations`、`/v1/videos/{id}` 和 `/v1/videos/{id}/content`，包括画面比例、分辨率、单图及多参考图。画布项目、密钥、生成记录保存在客户浏览器本地；没有引入远端提示词源、WebDAV、统计或任意第三方插件执行。

服务器只拉取构建好的镜像，不运行 npm、Go 编译。发布须先备份并检查数据库迁移兼容性，启动新容器并检查健康状态后平滑切换代理，等待旧连接结束再停旧容器；保留旧镜像和代理配置以便回退。
