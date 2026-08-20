# leasereg — 资源租约管理服务

leasereg 是一个**资源租约登记中心**：为命名资源（如打印机、分片、互斥临界区）授予
**有时限的排他租约**，并通过**单调递增的围栏令牌（fencing token）**防止持有者过期后
仍以旧令牌悄悄续期一个已被回收/重新分配的租约。

## 解决的业务问题

- 多个工作进程需要互斥地使用某个命名资源一段时间（例如独占打印队列、独占分片写入）。
- 仅靠内存锁无法跨进程崩溃恢复：进程重启后必须知道“这个资源还被人占着吗、占到几点”。
- 仅靠 TTL 过期还不够安全：持有者持有的令牌若被回收后又被重新分配，旧令牌必须被拒绝，
  否则会出现“过期持有者悄悄续期”的并发错误。

## 主要输入与输出

服务对外暴露 JSON/HTTP：

| 方法   | 路径                         | 作用                                       |
| ------ | ---------------------------- | ------------------------------------------ |
| POST   | `/acquire`                   | 申请租约，返回 fencing_token 与过期时间     |
| POST   | `/renew`                     | 凭 fencing_token 续期                       |
| POST   | `/release`                   | 凭 fencing_token 释放                       |
| POST   | `/transfer`                  | 将租约转让给新持有者（分配新令牌）         |
| POST   | `/batch/acquire`             | 批量申请，每项独立事务                      |
| GET    | `/info?resource=`            | 查询某资源当前租约                         |
| GET    | `/leases`                    | 列出全部租约                               |
| GET    | `/leases/{resource}`        | RESTful 查询某资源租约                     |
| GET    | `/expired`                   | 列出已过期未清扫的租约                     |
| GET    | `/fencing?resource=`         | 预览某资源下一个 fencing token             |
| GET    | `/holders/{holder}/leases`  | 列出某持有者的租约                         |
| POST   | `/resources`                 | 注册资源元数据（max_ttl 约束）             |
| GET    | `/resources`                 | 列出已注册资源                             |
| GET    | `/resources/{name}`          | 查询资源元数据                             |
| PUT    | `/resources/{name}`          | 更新资源元数据                             |
| DELETE | `/resources/{name}`          | 删除资源元数据（有活跃租约时拒绝）         |
| POST   | `/admin/sweep`               | 清扫已过期租约                             |
| DELETE | `/admin/leases/{resource}`   | 管理员强制释放                             |
| GET    | `/stats`                     | 统计（活跃/过期/资源数/持有者数）          |
| GET    | `/version`                   | 版本信息                                   |
| GET    | `/health`                    | 健康检查                                   |

请求体示例：

```json
{"resource": "printer-1", "holder": "alice", "ttl_seconds": 60}
```

成功响应：

```json
{
  "resource": "printer-1",
  "holder": "alice",
  "fencing_token": 1,
  "acquired_at": 1735732800,
  "expires_at": 1735732860,
  "ttl_seconds": 60
}
```

冲突时返回 409 并附当前占用者；续期已过期租约返回 410；令牌/持有者不匹配返回 403。

## 持久化与重启恢复

所有租约与每个资源的围栏计数器都保存在单个 SQLite 文件中（`modernc.org/sqlite`，纯 Go，
无需 CGO、无需外部服务）。进程重启时重新打开同一文件：

- 未过期的租约**继续有效**（可直接被查询/冲突检测到）；
- 已过期的租约在启动恢复扫描（`RestartRecover`）中被清扫；
- **每个资源的围栏令牌计数器跨重启单调续号**，不会重置为 1。

## 本地命令

```bash
go build ./...      # 编译
go run . --smoke-test   # 自检：acquire/renew/release/conflict/过期/围栏/重启恢复，打印 SMOKE_OK 后退出
go run . --addr :8080 --db leases.db   # 启动 HTTP 服务
go test ./...       # 测试
```

## Docker 构建

`build_benzhi_docker.sh` 接受两个参数：镜像名与目标平台。

```bash
# amd64
bash ./build_benzhi_docker.sh leasereg:amd64 linux/amd64
docker run -it leasereg:amd64

# arm64
bash ./build_benzhi_docker.sh leasereg:arm64 linux/arm64
docker run -it leasereg:arm64
```

容器内进入 shell 后可执行 `go build ./...`、`go test ./...` 或 `go run . --smoke-test`。

## 技术约束

- Go：`1.26.3`，`go.mod` 声明 `go 1.26.3`，`GOTOOLCHAIN=local`。
- SQLite：`3.46.1`（由 `modernc.org/sqlite v1.52.0` 内嵌，纯 Go，CGO_ENABLED=0）。
- 依赖通过 `GOPROXY=https://goproxy.cn,direct`、`GOSUMDB=sum.golang.google.cn` 获取。
- 交付 `linux/amd64` 与 `linux/arm64` 双架构镜像。
