# 可配置视频中转插件

该进程实现 `sub2-video-plugin/v1`（第一版视频插件协议），支持按账号选择火山方舟原生协议、万次元视频兼容协议或 OpenAI（开放接口）通用兼容视频中转协议。多个视频插件账号可以分别连接不同中转站，不需要为每个账号修改主程序。

插件按“供应商协议”适配，不按单个模型写死：同一中转站中共用相同创建、查询和下载协议的模型都可以由账号同步或手工加入。万次元和通用兼容模式会从账号配置的模型列表接口动态读取模型；同步后以供应商实时清单替换该账号的旧视频白名单。供应商没有模型列表接口时，可在账号白名单中手工添加任意真实模型标识，创建请求会原样转发或按账号模型映射转换。

## 主程序调用协议

- `GET /plugin/v1/manifest`：插件元数据与模型列表；兼容中转模式会读取供应商的模型列表接口。
- `POST /plugin/v1/videos`：创建视频任务。
- `GET /plugin/v1/videos/{id}`：查询任务状态。
- `GET /plugin/v1/videos/{id}/content`：代理下载成品。

内部请求通过 `X-Sub2-Plugin-Token`（插件共享令牌请求头）校验。上游密钥由 Sub2API 账号凭证中的 `api_key`（接口密钥）提供，只在请求头中转发，不写入插件配置或日志。

## 账号配置

- 平台：`video_plugin`
- 类型：`apikey`
- `base_url`：本次容器部署填写 `http://video-plugin-volcengine:8081`；仅非容器同机部署使用 `http://127.0.0.1:8081`
- `api_key`：当前账号对应视频中转站的接口密钥
- `plugin_token`：同机部署可留空，由主程序读取服务器环境变量；远程插件填写与 `VIDEO_PLUGIN_SHARED_SECRET` 一致的值
- `provider_protocol`：`volcengine_ark`（火山方舟原生）、`waninter`（万次元图片字段兼容）或 `openai_compatible`（OpenAI 兼容视频中转）
- `provider_base_url`：兼容中转站的接口地址，例如 `https://api.waninter.com`
- `provider_create_path`：默认 `/v1/videos/generations`
- `provider_status_path`：默认 `/v1/videos/{id}`
- `provider_content_path`：默认 `/v1/videos/{id}/content`
- `provider_models_path`：默认 `/v1/models`

万次元协议默认使用 `/v1/videos` 和 `/v1/videos/{id}`，并把统一视频请求转换为万次元实际使用的 `seconds`、`aspect_ratio` 与 `images` 字段。插件不再根据模型名猜测协议；只有账号明确把创建路径配置为 `/v1/contents/generations/tasks` 时，才会转换成火山方舟风格的 `content` 多模态任务。

兼容模式会先从查询接口读取成品地址；如果查询接口没有地址，会按 `provider_content_path` 再请求一次。内容接口既可以返回包含 `video_url`/`url` 的 JSON（结构化数据），也可以直接返回视频二进制。

公开调用仍使用 Sub2API 的统一接口：

```http
POST /v1/videos/generations
Authorization: Bearer <SUB2_API_KEY>
Content-Type: application/json

{
  "model": "doubao-seedance-2-5-260628",
  "prompt": "电影感运镜，人物向镜头走来",
  "image": "https://example.com/first-frame.png",
  "duration": 5,
  "ratio": "16:9",
  "resolution": "1080p",
  "generate_audio": true
}
```

创建成功后使用 `GET /v1/videos/{id}` 轮询；完成后通过响应中的代理地址或 `GET /v1/videos/{id}/content` 下载。

## 接入其他视频中转站

对于万次元或提供 OpenAI 兼容视频任务接口的中转站，在管理端创建“视频插件”账号时：

1. `base_url`（插件地址）填写 `http://video-plugin-volcengine:8081`（容器内部插件地址）。
2. 万次元选择“万次元视频兼容”，其它兼容中转站选择“OpenAI 兼容视频中转”。
3. 供应商接口地址填写中转站地址，例如 `https://api.waninter.com`。
4. 接口密钥填写该中转站的密钥，不要填写火山方舟密钥。
5. 按中转站文档调整创建、查询、下载和模型列表路径；不填写时使用上面的默认路径。若文档给的是 `/v1/videos`，就把创建任务路径改成 `/v1/videos`。
6. 点击“同步上游模型”，只把该中转站返回的模型加入这个账号的模型白名单。

万次元模式会把统一请求里的图片对象转换为万次元要求的图片地址字符串；通用兼容模式保留原始图片结构。同一个插件进程可以服务多个账号；每个账号通过自己的 `provider_base_url`（供应商地址）和 `provider_protocol`（供应商协议）独立路由。若供应商不是上述两种协议，则需要新增一个同样实现四个内部接口的专用适配器：清单、创建、查询和内容下载。

注意：兼容模式透传统一的视频任务 JSON（结构化请求）。如果中转站要求完全不同的请求体，例如只接受 `messages`（消息数组）而不接受 `prompt`（提示词），还需要为该中转站增加请求体转换规则，不能只改地址。

## 部署与计费

插件只接入 Docker 内网，不开放公网端口。主站和插件共享 `VIDEO_PLUGIN_SHARED_SECRET`，插件的 `/data/tasks` 必须挂持久化卷，以便重启后恢复供应商任务路径。任务按主站账号、供应商地址和协议隔离，48 小时后过期，文件不保存供应商密钥。

在生成前，为视频插件分组设置对应模型和分辨率的每秒价，或设置明确的渠道模型价格。没有配置价格时主站拒绝创建任务。完成后的轮询、下载共用一次计费标识；供应商模型映射不会覆盖客户模型的价格。兼容供应商返回的用量、成品时长会保留用于计费。
