# 流式响应完成后延迟结束：2026-09-21 调查

## 结论与范围

捕获的慢请求在文本和 `response.completed` 已发出后，仍等待几十秒乃至数分钟才结束 HTTP 响应。实际等待点在 Go HTTP/1 Transport 的响应体 EOF 通知；应用扫描器等待它退出，导致 usage duration 也计入收尾等待。

这部分线上流处理、转发与 HTTP Transport 源码没有被本站二开修改：共同祖先到原 fork/main 的 `openai_gateway_response_handling.go`、`openai_gateway_forward.go`、`repository/http_upstream.go`、`go.mod` 差异为空。不能把问题直接归咎图片工作台、用户名或页面列顺序的修改。

本地已复现“响应体读取与 Close 并发 + HTTP/1 连接复用”产生相同 EOF 等待。一次提前关闭能影响后续正常读完的请求。取消该请求上下文后再 Close 的对照没有出现长等待。这确定了可复现的触发机制，但尚未抓到生产中每个异常连接最早的 Close 调用；不能把所有历史卡顿都归为同一个原因，也不能声称官方更新已经解决。

## 生产对照

从同一服务器发起相同参数的独立合成请求，比较直连上游与本机中转服务。提示词为列出 1–180，stream=true、max_output_tokens=650、reasoning=medium、store=false。时间为各自请求开始后的 monotonic 秒数；独立生成结果有差异，不能直接相减总生成时长当成开销。

| 路径/模型 | 首段文字 | 最后文字 | response.completed | HTTP EOF |
| --- | ---: | ---: | ---: | ---: |
| 直连 / terra | 1.167 | 20.474 | 20.700 | 20.745 |
| 本机中转 / terra | 1.296 | 20.641 | 21.173 | 74.553 |
| 直连 / sol | 5.674 | 15.169 | 15.346 | 15.350 |
| 本机中转 / sol | 7.419 | 16.991 | 17.160 | 207.017 |

本机中转绕过了客户端公网路径和 Nginx，仍出现 completed→EOF 53.380s / 189.857s 的等待。等待期间没有新增 data 事件。使用记录 duration 包含这段等待，所以显示的 tokens/s 会被拉低；按完成事件结束的客户端可能不等待 EOF，按 HTTP EOF 结束的客户端则会受到影响。

这些样本足以证明中转服务存在额外收尾延迟；不代表上游所有请求都正常，也不代表所有客户都只受这个问题影响。

## 精确等待位置

另一条样本在 20.7266s 收到完成事件，64.1778s 才 EOF。使用短期函数入口跟踪与只读运行时栈检查，得到调用链：

```text
runtime.chanrecv1
net/http.(*persistConn).readLoop.func4        transport.go:2575
net/http.(*bodyEOFSignal).Read
bufio.(*Scanner).Scan
openAISSEJSONDocumentScanner.Scan
handleStreamingResponseWithReasoning 的读流协程
```

主处理协程同时停在扫描事件 select；读流退出后 finalize、return 和 affinity bind 启动在约 0.1ms 内发生，Redis 操作约毫秒级。证据不支持把这段几十秒等待归为 Redis 或数据库写入。

Go 1.27.0 相应源码只有在响应体已返回 `io.EOF` 后才等待 `eofc`，所以该样本并非仍在网络上等待模型输出或上游漏发结束事件。

HTTP/1 readLoop 为连接共用 `eofc`。提前 Close 发出 false 并等待通知，readLoop 可以尝试 drain 后复用连接；若原 reader 同时最终读到 EOF，它还可能发出 true 并等待通知。实验中这一组合能让旧 reader 等待后续响应的通知，并将等待传递到后续正常请求。仅在完成事件处调用 Body.Close 不能可靠解决这个问题。

## 无业务依赖的复现实验

运行：

```sh
go run tools/diagnostics/http-eof/main.go
```

程序只访问自身创建的 localhost HTTP/1 SSE 服务，不需要密钥、不连接上游、不改应用配置。12 个工作协程、每模式 720 个请求、最多 4 个复用连接；每四个请求中一个执行并发 Close，其余正常读完。长等待定义为首条完成事件之后超过 100ms。

Windows / Go 1.27.0 一次实测：

| 模式 | 长等待 | 其中正常 reader 长等待 | 请求失败 |
| --- | ---: | ---: | ---: |
| 全部正常读到 EOF、复用连接 | 0/720 | 0 | 0 |
| 混合并发 Close、复用连接 | 267/720 | 156 | 0 |
| 混合 cancel 后 Close、复用连接 | 0/720 | 0 | 0 |
| 混合并发 Close、禁用复用 | 0/720 | 0 | 0 |

异常组的运行时采样同时观察到最多 12 个 `readLoop.func4` 等待栈。对照组偶尔出现瞬时 EOF 通知等待，不能把单次短暂栈采样当作长延迟。

取消对照会产生预期的 reader canceled/closed 错误（一次 179/180 个被选中关闭的请求），这些不是正常请求的失败。数量受线程调度影响，复现脚本是诊断工具，不把概率结果作为稳定 CI 断言。

随后将同一程序在本机交叉编译为 Linux 二进制，以低优先级在服务器执行，仅连接程序自己的 localhost 测试端口（没有调用生产 API、没有在服务器编译）。Linux / Go 1.27.0 结果：正常读完 0/720 长等待；混合并发 Close 201/720 长等待，其中正常 reader 为 100；cancel-before-Close 和禁用复用两组均为 0/720。异常组同样捕获最多 12 个 EOF 通知等待栈。独立 Linux 复现说明现象不只发生在 Windows 测试环境。

## 后续修复的验证要求

优先验证每个上游请求拥有独立可取消上下文，发生流超时/提前退出时先取消该请求，再关闭并等待 reader 退出；完整成功请求应继续正常复用连接，不全局禁用连接池。当前官方已有部分首输出超时保护路径使用 cancel-before-Close，但不能由此推断所有关闭路径已经受保护。

需要在应用级真实 Transport 测试覆盖：正常成功复用、客户端断开后的计费用量收集、首输出和流间隔超时、错误与重试、HTTP/2 和 TLS 指纹路径，以及 inFlight 计数/协程泄漏。之后再做隔离环境的前后对照，才能把候选修复标为完成。

本次提交提供诊断证据与独立复现工具，没有加入未经应用级验证的 Transport 行为修改，没有在线上关闭连接复用或改渠道。原始生产跟踪仅保存在本地；这里没有发布客户内容、密钥、服务器登录信息或原始内存转储。
