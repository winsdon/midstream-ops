# midstream-ops — 构建与运行
# 需要：Go 1.25+、Node 20+、pnpm 10+
#
# 注意：本文件是裸命令的可选封装，需本机已安装 GNU make（Windows 默认没有）。
# 未安装 make 时请直接用 README「开发模式」中的裸命令：
#   后端  cd backend && go run ./cmd/server
#   前端  cd frontend && pnpm dev

BINARY  := server
BACKEND := backend
FRONTEND := frontend
DIST    := $(BACKEND)/internal/web/dist

.PHONY: all dev backend frontend build build-embed run clean tidy

# 默认：完整构建单二进制（前端 → embed 编译）
all: build-embed

# 安装依赖
tidy:
	cd $(BACKEND) && go mod tidy
	cd $(FRONTEND) && pnpm install

# 前端开发服务器（:3001，代理 /api → :9090）
dev-frontend:
	cd $(FRONTEND) && pnpm dev

# 后端开发（无 embed，前端走 dev 服务器）
dev-backend:
	cd $(BACKEND) && go run ./cmd/server

# 仅构建前端产物 → backend/internal/web/dist
frontend:
	cd $(FRONTEND) && pnpm build

# 仅构建后端二进制（无 embed）
backend:
	cd $(BACKEND) && go build -o $(BINARY).exe ./cmd/server

# 完整构建：前端产物 + embed 单二进制
build-embed: frontend
	cd $(BACKEND) && go build -tags embed -o $(BINARY).exe ./cmd/server
	@echo "==> 单二进制: $(BACKEND)/$(BINARY).exe"

# 运行（需先 build-embed，且 backend/config.yaml 已配置）
run:
	cd $(BACKEND) && ./$(BINARY).exe

# 清理构建产物
clean:
	rm -rf $(DIST) $(BACKEND)/$(BINARY).exe
