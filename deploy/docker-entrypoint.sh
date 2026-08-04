#!/bin/sh
set -e

# 命名 volume 首次挂载时属主是 root，非 root 进程会在创建/打开 SQLite 时
# permission denied，进而触发 main.go 的 log.Fatalf 直接退出。
# 所以以 root 起步时先修属主，再降权。
if [ "$(id -u)" = "0" ]; then
    mkdir -p /app/data
    # 只读挂载（如 config.yaml:ro）会让 chown 失败，用 || true 放过
    chown -R monitor:monitor /app/data 2>/dev/null || true
    # 重新以 monitor 身份执行本脚本，让下面的 flag 判断也在正确用户下运行
    exec su-exec monitor "$0" "$@"
fi

# 兼容 `docker run <image> --config /path` 这种只传 flag 的用法：
# 首个参数看起来是 flag 时，补上默认二进制。
if [ "${1#-}" != "$1" ]; then
    set -- /app/monitor "$@"
fi

exec "$@"
