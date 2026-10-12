# 分组鹈鹕测试

管理端入口为 `/pelican`，客户菜单 URL 为 `https://你的监控站/embed/pelican`。
客户入口沿用现有 `plaza.enabled`、`plaza.sub2api_base_url`、`plaza.sub2api_jwt_secret` 设置及用户会话；无需另一套登录配置。

## 首次配置

1. 在 sub2api 准备可直接调用的上游账号（`api_key` 类型、`active`、配好 `base_url`），或在手填模式里自备网关地址与 Key。
2. 在管理端「鹈鹕测试 → 测试分组」添加测试目标，每行依次选择：
   - **归属分组**：作品挂到哪个分组下。客户页按分组筛选已发布作品，发布范围也按分组计算；分组不再决定凭据来源。
   - **凭据来源**：
     - **本站账号**：从已建档的 anthropic / openai 账号里选一个，后端用该账号的 `base_url` 与 Key 直连上游；列表只返回名称、平台与地址，Key 不出后端。
     - **手填地址 + Key**：自己填网关地址（可带 `/v1`，不要填具体生成接口路径）与 Key，直连该地址。
   - **模型**：从归属分组的可用模型里选，或直接手填。
   - **接口协议**：账号来源按账号平台限定（anthropic → Messages，openai → Responses / Chat Completions）；手填来源三种都可选。
3. 确认提示词后发起测试。提示词和测试目标在页面上方展开，一行一个目标；并发数、输出上限和单项超时在目标列表下方的操作条里。提交后作品列表会筛到这一批。
4. 预览作品，勾选后发布。客户可查看所有普通分组的已发布作品；专属、停用或已删除分组不展示。

手填来源的 Key 明文既不出现在任何接口响应里，也不写入任务表或结果表：落库的是 sha256 指纹，明文只在进程内用于发请求，因此**重新测试需要再填一次 Key**。同一批次内账号来源的 Key 也在批次开始时快照到内存。

## 执行与历史

- Claude 使用 Anthropic Messages；GPT 默认 OpenAI Responses，也可选择 Chat Completions。协议与账号平台必须匹配，不匹配的目标在提交时即被拒。
- 默认并发 3、每项超时 900 秒、输出上限 32,000 tokens；高级选项可调整。
- 默认提示词为 `生成一张考拉骑自行车的 SVG 图像并添加动画效果`，页面可以改写本轮提示词；实际使用的提示词随记录留痕。
- Messages 请求与模型检测的智商题共用一套 thinking 口径（`modeldetect.ThinkingParamFor`）：adaptive 型号发 `{"type":"adaptive"}`，旧型号带 `budget_tokens`，型号不认识或本来不支持 thinking（如 Haiku 3.5）时不发；Messages 同时复用同一套 `claude-cli` User-Agent。Opus / Sonnet 5 起不传也会思考且关不掉，思考量与正文共用 `max_tokens` 上限，输出上限给小了会在写完前被截断。
- 管理员填写的提示词作为用户消息原样发送；另附统一交付指令，要求在回复正文中直接返回完整独立 HTML，不依赖外部资源，不用文件路径或“已创建文件”的说明代替代码。Messages 使用 `system`，Responses 使用 `instructions`，Chat Completions 使用 system 消息。完整请求（含交付指令，不含 Key）保存在结果详情的请求参数中。不提供文件写入或工作区工具。
- Responses 流优先读取 `response.completed` 中的最终正文；完成事件没有正文时才使用累计文本增量，避免只收到前言增量而漏掉最终 HTML。
- 流式响应必须正常结束；截断、异常断流、无完整 HTML/SVG 都不允许发布。可在详情查看已收到的原始回复。
- 提交请求使用幂等 ID：参数未改动时「重试提交」沿用同一个 ID，不会重复执行；改了参数则算新测试。待确认提交写 sessionStorage 前会剥掉手填 Key，刷新后需重新填写 Key 才能再提交。
- 「重新测试」创建新批次和新结果，并关联原记录。账号来源可换一个账号重跑，手填来源需重新填 Key。它不会覆盖旧作品，也不会自动换协议或自动重发付费请求。改造前用「专用测试用户分组 Key」生成的旧记录不支持重新测试，仍可查看、撤下与删除。
- 取消会停止排队和正在读取的请求，但无法保证网关撤销已产生的用量。服务重启把遗留等待/运行任务标记为中断：进程内的凭据快照同时失效，不自动恢复调用。
- 发布与撤下支持批量且在同一事务中完成；删除仅允许非运行、未发布结果（已发布的先撤下）。卡片与详情都提供单条删除，未发布记录也可勾选后批量删除。
- 客户「最新作品」按分组、模型及实际测试时间选取最新已发布记录。日期筛选使用浏览器本地日期并转为 UTC 查询，页面显示时区。发布时间不改变测试顺序。
- 作品保留原始代码，不修补画面、不自动评估画质、不生成智商排名。历史默认长期保留，不受旧智商测试最近 20 条清理限制。

## 预览

作品渲染在 1280×800 的虚拟视口里，再整体等比缩放到卡片或详情框内：固定像素尺寸的 HTML 也能整幅看到，且作品自己的滚动条被关掉。因此内容高于 800px 的页面在预览里会被裁切（详情提供「源码」页签查看原文），依赖外部 CDN 的作品也可能显示不完整。动画在不具备同源权限的 iframe 中自动播放；进入视口加载、离开销毁，禁止外部脚本与资源加载。管理员应预览后再决定发布。

## 接口

管理员鉴权前缀 `/api/v1/pelican`：

- `GET /accounts`（可选账号，不含密钥）、`GET /groups`（可选归属分组与模型提示）
- `POST /batches`、`GET /batches/:id`、`POST /batches/:id/cancel`
- `GET /results`、`GET /results/:id`、`POST /results/:id/retry`
- `POST /results/actions/publish|unpublish|delete`，请求体 `{ "ids": [1, 2] }`

测试目标提交格式：`{ "group_id": 1, "source": "account" | "manual", "account_id": 7, "base_url": "https://api.example.com", "api_key": "...", "model": "...", "protocol": "messages" }`（`account_id` 与 `base_url`/`api_key` 按来源二选一）。`POST /results/:id/retry` 请求体 `{ "request_id": "...", "account_id": 7, "api_key": "..." }`：账号来源可省略 `account_id`（沿用原记录），手填来源必须带 `api_key`，旧记录返回 `pelican.errors.legacyRetry`。

嵌入会话前缀 `/api/v1/embed/pelican`：

- `POST /session`：交换 sub2api 用户 token。
- `GET /filters`、`GET /results`、`GET /results/:id`：需嵌入会话。

列表参数：`group_id`、`model`、`from`、`to`、`page`、`page_size`；日期为 RFC3339，`to` 为不含边界。客户默认最新模式，`history=true` 查询全部历史。管理员另支持 `batch_id`。列表不包含作品代码；详情按权限单独读取。

## 部署与验证

部署包含前端构建的新版本并重启后端；启动自动执行 monitor 库的 `009_pelican.sql` 迁移（本改造不新增迁移，`metadata` 为 JSONB）。任务执行器按本项目的单实例部署方式运行，不支持多个服务实例共享任务表并同时执行启动恢复。

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

请求交付方式参考 [cockpit-tools 的鹈鹕请求实现](https://github.com/jlcodes99/cockpit-tools/blob/4ea6a34df6aa3b2d494c3cca5983bd09a84a9e3a/src-tauri/src/modules/codex_pelican_transport.rs#L310)。本站按目标来源解析凭据后直连上游端点，不使用该项目的 Codex 账号登录与客户端身份机制，也不经由本站网关转发。
