# 分组鹈鹕测试

管理端入口为 `/pelican`，客户菜单 URL 为 `https://你的监控站/embed/pelican`。
客户入口沿用现有 `plaza.enabled`、`plaza.sub2api_base_url`、`plaza.sub2api_jwt_secret` 设置及用户会话；无需另一套登录配置。

## 首次配置

1. 在 sub2api 创建专用测试用户，为需要测试的分组创建 API Key，并给测试用户准备可用额度或订阅。
2. 在管理端「鹈鹕测试 → 测试配置」填写本站网关地址并选择该测试用户。地址可带 `/v1`，不能填写具体生成接口路径。
3. 添加测试目标，选择分组、Key 和模型；同一组测试多个模型时添加多行。只有一把 Key 自动选中，多把则手动选择。模型可以从分组列表选择或手填。
4. 确认提示词后发起测试。请求会产生实际用量，费用归专用测试用户。
5. 预览作品，勾选后发布。客户可查看所有普通分组的已发布作品；专属、停用或已删除分组不展示。

测试只读取 sub2api 用户、Key 和分组，所有任务与作品写入独立 monitor 库。Key 明文不会返回前端或写入任务表。

## 执行与历史

- Claude 使用 Anthropic Messages；GPT 默认 OpenAI Responses，也可选择 Chat Completions。混合分组按目标模型选择协议。
- 默认并发 3、每项超时 900 秒、输出上限 32,000 tokens；高级选项可调整。不强制开启额外思考参数。
- 管理员填写的提示词作为用户消息原样发送；另附统一交付指令，要求在回复正文中直接返回完整独立 HTML，不依赖外部资源，不用文件路径或“已创建文件”的说明代替代码。Messages 使用 `system`，Responses 使用 `instructions`，Chat Completions 使用 system 消息。完整请求（含交付指令）保存在结果详情的请求参数中。不提供文件写入或工作区工具。
- Responses 流优先读取 `response.completed` 中的最终正文；完成事件没有正文时才使用累计文本增量，避免只收到前言增量而漏掉最终 HTML。
- 流式响应必须正常结束；截断、异常断流、无完整 HTML/SVG 都不允许发布。可在详情查看已收到的原始回复。
- 提交请求使用幂等 ID，网络异常时「重试提交」沿用该 ID。尚未确认提交时，不要清除待确认提交后反复新建，以免人为发起多次测试。
- 「重新测试」创建新批次和新结果，并关联原记录。它不会覆盖旧作品，也不会自动换协议或自动重发付费请求。
- 取消会停止排队和正在读取的请求，但无法保证网关撤销已产生的用量。服务重启把遗留等待/运行任务标记为中断，不自动恢复调用。
- 发布与撤下支持批量且在同一事务中完成；删除仅允许非运行、未发布结果。
- 客户「最新作品」按分组、模型及实际测试时间选取最新已发布记录。日期筛选使用浏览器本地日期并转为 UTC 查询，页面显示时区。发布时间不改变测试顺序。
- 作品保留原始代码，不修补画面、不自动评估画质、不生成智商排名。历史默认长期保留，不受旧智商测试最近 20 条清理限制。

动画在不具备同源权限的 iframe 中自动播放；进入视口加载、离开销毁，禁止外部脚本与资源加载。因此依赖外部 CDN 的作品可能显示不完整，管理员应预览后再决定发布。

## 接口

管理员鉴权前缀 `/api/v1/pelican`：

- `GET/PUT /config`、`GET /groups`
- `POST /batches`、`GET /batches/:id`、`POST /batches/:id/cancel`
- `GET /results`、`GET /results/:id`、`POST /results/:id/retry`
- `POST /results/actions/publish|unpublish|delete`，请求体 `{ "ids": [1, 2] }`

嵌入会话前缀 `/api/v1/embed/pelican`：

- `POST /session`：交换 sub2api 用户 token。
- `GET /filters`、`GET /results`、`GET /results/:id`：需嵌入会话。

列表参数：`group_id`、`model`、`from`、`to`、`page`、`page_size`；日期为 RFC3339，`to` 为不含边界。客户默认最新模式，`history=true` 查询全部历史。管理员另支持 `batch_id`。列表不包含作品代码；详情按权限单独读取。

## 部署与验证

部署包含前端构建的新版本并重启后端；启动自动执行 monitor 库的 `009_pelican.sql` 迁移。任务执行器按本项目的单实例部署方式运行，不支持多个服务实例共享任务表并同时执行启动恢复。

验证命令：

```sh
cd backend
go test ./...
cd ../frontend
pnpm typecheck
pnpm test
pnpm build
```

涉及 PostgreSQL 的测试要求 `MONITOR_TEST_PG_DSN` 指向独立测试库；测试会创建临时 schema、执行迁移并清理。未设置时明确跳过。网关测试全部使用本地模拟响应，不调用真实付费模型。

请求交付方式参考 [cockpit-tools 的鹈鹕请求实现](https://github.com/jlcodes99/cockpit-tools/blob/4ea6a34df6aa3b2d494c3cca5983bd09a84a9e3a/src-tauri/src/modules/codex_pelican_transport.rs#L310)。本站通过 sub2api 分组 Key 请求网关，不使用该项目的 Codex 账号登录与客户端身份机制。
