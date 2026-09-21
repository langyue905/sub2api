# 2026-09-21 官方更新与二开功能核对

本次在 `integration/upstream-20260921` 分支整合，不更新生产网站、不发布版本、不执行数据库迁移。

## 版本依据

- 原 fork/main：`061d30ff1f77fc1d2e3af8e1528bb89648cb3dd7`。
- 官方 Wei-Shaw/sub2api/main：`7c700729c23187d31ed320f6b19c790e2f194826`。
- 共同祖先：`b1748c4ea99ce2120401a269142aa071e18a84da`；合入官方新增 553 个提交。
- 使用真正的 merge commit 保留两边历史，不用官方目录覆盖 fork。
- 唯一文本冲突是 `backend/cmd/server/VERSION`：保留 fork 的 `0.2.11`。这不代表本次已经发布。
- 调查所用生产 revision 为 `38115441e56e0514a9e8b326e1164fe41a36002b`；与上述 fork/main 的源码差异只有 VERSION。

## 保留的二开功能

| 功能 | 核对内容 |
| --- | --- |
| 图片工作台 | `/image-playground` 路由、侧栏、嵌入 `/playgrounds/image/`、锁定站点 API 地址保留。完整静态资源目录与更新前 Git tree 一致。 |
| 图生图及下载修复 | 原有 fix4 脚本、Data URL 转 Blob、IndexedDB 原图恢复、上传处理、下载 CSP、嵌入目录 index 处理保留。 |
| 用户名注册与登录 | 普通注册要求用户名，邮箱验证流程携带用户名；用户名/邮箱登录、唯一性检查和规范化查询保留。OAuth 兼容路径保留。 |
| 用户管理 | 用户名列在前；原“用户”邮箱列仍标为“用户邮箱”，与用户名的位置调整保留。 |
| 使用记录 | 用户列显示用户名；“账户”继续指号池/上游账户。显示与导出顺序均为用户名、API Key、账户，后面接模型等字段。 |
| 排行与错误记录 | 优先显示用户名，缺少用户名时回退邮箱。 |
| 邀请返利 | 官方 affiliate 管理、手动绑定邀请人及历史代理迁移脚本保留；不恢复已移除的旧代理中心。 |
| 模型广场 | 显式配置价格显示渠道填写值，纯模型映射保留目录参考价回退。 |

图片工作台资源目录在合并前后的 tree ID 都是：

```text
5737838fbfb4d0b188cb7c156f4c64fd45bbdf72
```

可复查：

```sh
git diff 061d30ff1 integration/upstream-20260921 -- frontend/public/playgrounds/image
git diff upstream/main integration/upstream-20260921 -- frontend/src/views/auth backend/internal/service/auth_service.go
```

## 合并后的测试适配

官方新增的注册密码确认测试没有填写二开必填用户名，已补上用户名并检查其传递，不删除用户名要求。新增用户名/邮箱登录回归测试。

将注册、登录、邮箱验证、用户管理、使用记录、排行、错误日志和 auth store 的九组测试加入 `make test-frontend-critical`，GitHub CI 后续持续检查这些功能。

官方 Ollama 过期回调测试用两次紧邻的 `time.Now()` 模拟不同代次，在 Windows 粗粒度时钟下可能得到相同值；改成基于已保存的时间明确增加 1ms，保持测试原有“旧回调不能覆盖新状态”的语义，不改业务逻辑。

## 验证与发布边界

- 前端 frozen lockfile 安装使用 pnpm 9.15.9，与官方 CI 的 pnpm 9 一致；不重写锁文件。
- 前端 lint、类型检查和完整生产构建已通过；关键测试及二开测试已检查。构建产物未上传生产服务器。
- 后端 Go 1.27.0 单元测试在本机运行；Windows 缺少 `sh` 导致的备份测试失败，通过仅在测试进程 PATH 加入 Git 自带 shell 解决。
- GitHub Linux CI 检查后端单元/集成测试、golangci-lint、前端、部署脚本与发布辅助脚本。最终结果以 PR checks 为准。
- 生产流式结束延迟证据和实验见 [调查报告](stream-eof-investigation-20260921.md)。此次官方更新本身不作为延迟已修复的依据。
- 本次没有修改线上渠道、重启/替换生产服务、运行生产迁移或在服务器编译。下一次部署前仍需单独验证升级迁移和回滚方案。
