# CLAUDE.md - DistributedDetectionNode 项目规则

## 项目概述

DistributedDetectionNode 是一个分布式检测节点服务，用于 DeepBrainChain (DBC) 网络。提供 HTTP/WebSocket 服务，用于收集和监控节点健康数据。

## 技术栈

- **语言**: Go 1.22.9
- **Web框架**: Gin
- **WebSocket**: gorilla/websocket
- **数据库**: MongoDB 7.0+ (时序集合)
- **监控**: Prometheus
- **区块链**: Ethereum (go-ethereum)
- **IP定位**: IP2Location

## 项目结构

```
DistributedDetectionNode/
├── app/                    # 应用入口
│   ├── ddn/               # 主程序入口
│   └── bandwidth/         # 带宽测试工具
├── db/                     # 数据库层
│   ├── mongo.go           # MongoDB 操作
│   └── ip2location.go     # IP 地理位置查询
├── dbc/                    # DBC 链相关
│   ├── dbc.go             # DBC 核心功能
│   ├── calculator/        # 计算器服务
│   ├── machine-infos/     # 机器信息合约
│   └── ai-report/         # AI 报告合约
├── http/                   # HTTP 服务
│   ├── query.go           # 查询接口
│   ├── prometheus.go      # Prometheus 指标
│   ├── status.go          # 状态接口
│   └── contract.go        # 合约相关接口
├── ws/                     # WebSocket 服务
│   ├── hub.go             # 连接管理中心
│   ├── client.go          # 客户端连接处理
│   └── echo.go            # Echo 测试
├── types/                  # 类型定义
│   ├── config.go          # 配置结构
│   ├── ws.go              # WebSocket 消息类型
│   ├── http.go            # HTTP 请求/响应类型
│   ├── mongo.go           # MongoDB 文档类型
│   └── contract.go        # 合约类型
├── log/                    # 日志模块
│   └── log.go             # 日志配置
├── go.mod                  # Go 模块定义
├── go.sum                  # 依赖校验
└── gpus.json              # GPU 配置数据
```

## 构建命令

```shell
# 本地构建
go build -ldflags "-X main.version=v0.0.1" -o ddn ./app/ddn/ddn.go

# Linux 交叉编译
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=v0.0.1" -o ddn ./app/ddn/ddn.go
```

## 运行命令

```shell
ddn -config ./config.json
```

## 代码规范

1. **错误处理**: 使用 `types/error.go` 中定义的错误类型
2. **日志记录**: 使用 `log/log.go` 中的 logrus 配置
3. **配置管理**: 所有配置通过 JSON 文件加载，结构定义在 `types/config.go`
4. **命名规范**:
   - 文件名使用小写下划线命名
   - 包名使用小写字母
   - 导出函数使用大驼峰命名

## WebSocket 消息类型

| Type | 描述 |
|------|------|
| 0 | 保留 |
| 1 | 上线消息 (Online) |
| 2 | DeepLink 短租设备信息 |
| 3 | 通知消息 |
| 4 | DeepLink 带宽挖矿设备信息 |

## 重要依赖

- MongoDB 7.0+ (时序集合功能需要)
- IP2Location 数据库文件

## 测试

```shell
go test ./...
```

## 变更日志

### 2026-01-08
- 初始化 CLAUDE.md 文档
- 项目结构: Go 1.22.9, Gin, MongoDB, WebSocket
- 当前分支: main
