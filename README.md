# AgentOps

LLM 调用观测与评测平台（对标 LangSmith / Langfuse）。追踪大模型调用的完整链路，管理 Prompt 版本，并对 Prompt 进行批量评测与多版本对比。

## 功能

- **Trace 追踪**：记录每次 LLM 调用的输入 / 输出 / token 消耗 / 延迟 / 状态，支持按用户、模型筛选与分页
- **Prompt 版本管理**：一个 Prompt 多个版本，支持创建、切换版本
- **Evaluation 评测**：数据集 + 批量评测 + LLM-as-Judge 打分 + 多版本对比

## 技术栈

- **Go + Gin**：HTTP 服务与路由
- **MySQL 8.4**（InnoDB）：数据存储，事务 / 行锁 / 连接池
- **Redis 7**：Cache-Aside 缓存 + 固定窗口限流
- **Docker Compose**：一键起 MySQL + Redis
- **DeepSeek API**：LLM 生成与 LLM-as-Judge 评测

## 快速开始

```powershell
# 1. 启动 MySQL + Redis（Docker）
docker compose up -d --wait

# 2. 设置 DeepSeek API Key（评测功能需要）
$env:DEEPSEEK_API_KEY = "sk-xxxx"

# 3. 启动服务
go run .

# 4. 运行测试（全量 + 竞态检测）
go test ./... -race
```

服务启动后访问 <http://127.0.0.1:8000/healthz> 探活，访问 <http://127.0.0.1:8000/api/v1> 查看接口。

数据库 / Redis 连接参数均来自环境变量并有默认值，默认值已对齐 `docker-compose.yml`（见 `config/config.go`），本地开发无需额外配置。

## 架构

```
handler（HTTP 接口层）
    ↓
service（业务编排：评测流程、LLM 调用）
    ↓
store（MySQL 数据访问）      cache（Redis 缓存 / 限流）
```

分层原则：handler 只做参数校验与响应，service 负责业务编排，store 只做数据访问、不含业务规则（如检索打分这类逻辑放在 service 层）。

## 技术亮点

- **并发版本号竞态的三层防护**：事务 + `SELECT ... FOR UPDATE` 行锁 + 唯一键 `(prompt_id, ver)` 兜底
- **Cache-Aside 缓存**：写后删缓存（幂等），Redis 故障自动降级直查 MySQL，业务不中断
- **固定窗口限流**：Redis `INCR + EXPIRE`，fail-open 降级（可用性优先）
- **连接池参数**：`ConnMaxLifetime < wait_timeout`，避免拿到被服务端悄悄关闭的"死连接"
- **并发评测**：goroutine + `WaitGroup`，每 goroutine 写独立下标实现无锁并发

## 接口一览（18 个）

| 模块 | 接口 |
| --- | --- |
| Trace | `POST /api/v1/traces` · `GET /api/v1/traces/:id` · `GET /api/v1/traces` |
| Prompt | `GET/POST /api/v1/prompts` · `GET /api/v1/prompts/:id` · `POST /api/v1/prompts/:id/versions` · `PUT /api/v1/prompts/:id/versions/:ver` |
| Dataset | `GET/POST /api/v1/datasets` · `GET /api/v1/datasets/:id` · `POST /api/v1/datasets/:id/items` · `DELETE /api/v1/datasets/:id/items/:item_id` · `DELETE /api/v1/datasets/:id` |
| Evaluation | `GET/POST /api/v1/evaluations` · `GET /api/v1/evaluations/:id` · `POST /api/v1/evaluations/compare` |

## 目录结构

```
├── main.go              # 入口：路由注册 + 优雅关闭
├── handler/             # HTTP 处理器
├── service/             # 业务逻辑（评测编排、LLM 调用）
├── store/               # MySQL 数据访问（建表 / 增删改查）
├── cache/               # Redis 封装（缓存 + 限流计数）
├── middleware/          # 限流中间件
├── model/               # 数据模型
├── config/              # 配置（环境变量 + 默认值）
├── internal/            # 测试辅助工具
├── deploy/initdb/       # 建表初始化脚本
└── docker-compose.yml   # MySQL + Redis 本地环境
```

## 测试

- `go test ./... -race` 全量通过（竞态检测）
- 缓存 / 限流用 [miniredis](https://github.com/alicebob/miniredis) 内存单测
- MySQL 不可达时，相关测试自动跳过（`internal/testutil`）
