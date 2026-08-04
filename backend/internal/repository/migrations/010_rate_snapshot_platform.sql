-- 分组按平台分类：rate_snapshots 记录上游分组所属平台
--
-- 上游 /api/v1/groups/available 本就返回 platform（anthropic/openai/gemini/...），
-- 此前落库时丢弃。补上后「可用分组」弹窗可按平台分节展示。
--
-- 注意：platform 不参与倍率变化判定（见 rate_service.go Reconcile）。
-- 快照行的语义是「一次真实的倍率变化」，platform 只是随行的描述属性，
-- 与 name 一样走 Touch 同步更新，绝不因其变动而插入新行。
--
-- new-api 平台的分组接口无此字段，留空串，前端归入「未分类」。

ALTER TABLE rate_snapshots ADD COLUMN platform TEXT NOT NULL DEFAULT '';
