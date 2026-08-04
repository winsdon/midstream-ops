-- 站点级忽略余额告警：providers.ignore_balance_alert
--
-- 此前余额告警只有全局开关（settings.balance_alert_enabled），粒度太粗：
-- 某个长期低余额或待弃用的站点会在冷却到期后反复触发，逼得用户关掉全局开关，
-- 把其他站点的告警一并牺牲。加一位站点级静音，让单站可独立退出告警。
--
-- 只静音余额告警，不影响余额采集本身——快照照常写入，站点数据仍可见，
-- 只是不再推送通知（见 alert_service.go HandleSyncOutcome）。

ALTER TABLE providers ADD COLUMN ignore_balance_alert INTEGER NOT NULL DEFAULT 0;
