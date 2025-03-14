# DistributedDetectionNode

分布式节点健康监控平台。

## 设计方案

1. 提供 HTTP/Websocket 服务，可以用 Websocket 是否连接作为是否在线的判断依据。
2. 使用 MongoDB 数据库存储客户端推送的指标数据，支持水平扩展的集群服务。
3. 提供一些接口可以为 Prometheus 提供数据。
4. 提供一些管理接口或功能，如数据清理、持久化等。

- [MongoDB 时间序列](https://www.mongodb.com/zh-cn/products/capabilities/time-series)
- [MongoDB 时间序列用户文档](https://www.mongodb.com/zh-cn/docs/manual/core/timeseries-collections/)
- [MongoDB Go Driver 时间序列集合](https://www.mongodb.com/zh-cn/docs/drivers/go/current/fundamentals/time-series/)
- [Prometheus Pushgateway](https://github.com/prometheus/pushgateway)

备注: MongoDB 5.0 版本太低，时间序列功能不全，还有 Bug，需要使用最新的 7.0 以上版本。

```shell
docker pull mongodb/mongodb-community-server:7.0.12-ubuntu2204
docker run --name mongodb -p 27017:27017 -d mongodb/mongodb-community-server:7.0.12-ubuntu2204
```

## Build

```shell
go build -ldflags "-X main.version=v0.0.1" -o ddn ./app/ddn/ddn.go
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=v0.0.1" -o ddn ./app/ddn/ddn.go
```

## Run

创建一个 JSON 配置文件，示例如下:

```json
{
  "Addr": "0.0.0.0:9521",
  "LogLevel": "info",
  "LogFile": "./test.log",
  "MongoDB": {
    "URI": "mongodb://127.0.0.1:27017/",
    "Database": "health_monitoring",
    "ExpireTime": 86400
  },
  "IP2LocationDB": {
    "DatabasePath": "./IP2LOCATION-LITE-DB5.BIN/IP2LOCATION-LITE-DB5.BIN"
  },
  "Prometheus": {
    "JobName": "test"
  },
  "Chain": {
    "Rpc": "https://rpc-testnet.dbcwallet.io",
    "ChainId": 19850818,
    "PrivateKey": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "ReportContract": {
      "AbiFile": "./dbc/ai_abi.json",
      "ContractAddress": "0xb616A0dad9af4cA23234b65D27176be2c09c720c"
    },
    "MachineInfoContract": {
      "AbiFile": "./dbc/0xE676096cA8B957e914094c5e044Fcf99f5dbf3C0.json",
      "ContractAddress": "0xE676096cA8B957e914094c5e044Fcf99f5dbf3C0"
    }
  },
  "NotifyThirdParty": {
    "OfflineNotify": "https://nodeapi.deeplink.cloud/api/cyc/notifyOffline"
  }
}
```

使用命令 `ddn -config ./config.json` 运行即可。

程序会启动一个 WebSocket 服务，可以使用 `ws://192.168.1.159:9521/websocket` 连接。

## WebSocket

WebSocket 设置了心跳服务，即 client 发送 ping 消息，服务回复 pong 消息。
如果 30s 内没有任何 ping 消息，连接将被服务端断开。
请及时发送 ping 消息，既是一种心跳，又能保证长连接的稳定可靠。

WebSocket 消息采用 UTF-8 文本格式，主要使用 JSON 形式。具体示例请看 [测试用例](./ws/ws_test.go)

client 向 server 发送的请求消息主要由 Header 和 Body 两部分组成。

<table>
  <tr>
    <td></td>
    <td>字段</td>
    <td>描述</td>
    <td>类型</td>
    <td>备注</td>
  </tr>
  <tr>
    <td rowspan="6">Header</td>
    <td>version</td>
    <td>协议版本，暂时用 0</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>timestamp</td>
    <td>时间戳</td>
    <td>int64</td>
    <td></td>
  </tr>
  <tr>
    <td>id</td>
    <td>消息序号，一对请求与应答的序号相同</td>
    <td>uint64</td>
    <td></td>
  </tr>
  <tr>
    <td>type</td>
    <td>消息体的类型，0 - 保留， 1 - online，2 - 机器信息</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>pub_key</td>
    <td>公钥，验证消息安全完整，暂时不需要</td>
    <td>[]byte</td>
    <td></td>
  </tr>
  <tr>
    <td>sign</td>
    <td>签名，验证消息安全完整，暂时不需要</td>
    <td>[]byte</td>
    <td></td>
  </tr>
  <tr>
    <td>Body</td>
    <td>body</td>
    <td>消息体，真正的消息通过 JSON 编码，加密后的字节数组</td>
    <td>[]byte</td>
    <td></td>
  </tr>
</table>

消息体暂时有以下几种:
- 0 - 没有意义
- 1 - Online，表示 WebSocket 连接属于那个设备或者节点。
```json
{
  "machine_id": "123456789",
  "project": "deeplink",
  "staking_type": 0
}
```
- 2 - DeepLink 短租设备信息，定时发送显卡等机器信息。
```json
{
  "cpu_type": "13th Gen Intel(R) Core(TM) i5-13400F",
  "cpu_rate": 2500,
  "gpu_names": [
    "NVIDIA GeForce RTX 4060"
  ],
  "gpu_memory_total": [
    8
  ],
  "memory_total": 16,
  "wallet": "xxxxxx"
}
```
- 3 - 通知消息。
```json
{
  "unregister": {
    "message": "machine unregistered, notify from node server"
  }
}
```
- 4 - DeepLink 带宽挖矿设备信息。
```json
{
  "cpu_cores": 1,
  "memory_total": 2,
  "hdd": 50,
  "bandwidth": 10,
  "wallet": "xxxxxx"
}
```

server 向 client 返回的应答消息体格式结构相似，只比请求多了 Code 和 Message 两个字段。

<table>
  <tr>
    <td></td>
    <td>字段</td>
    <td>描述</td>
    <td>类型</td>
    <td>备注</td>
  </tr>
  <tr>
    <td rowspan="6">Header</td>
    <td>version</td>
    <td>协议版本，暂时用 0</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>timestamp</td>
    <td>时间戳</td>
    <td>int64</td>
    <td></td>
  </tr>
  <tr>
    <td>id</td>
    <td>消息序号，一对请求与应答的序号相同</td>
    <td>uint64</td>
    <td></td>
  </tr>
  <tr>
    <td>type</td>
    <td>消息体的类型，与请求的类型相同</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>pub_key</td>
    <td>公钥，验证消息安全完整，暂时不需要</td>
    <td>[]byte</td>
    <td></td>
  </tr>
  <tr>
    <td>sign</td>
    <td>签名，验证消息安全完整，暂时不需要</td>
    <td>[]byte</td>
    <td></td>
  </tr>
  <tr>
    <td>Code</td>
    <td>code</td>
    <td>错误码，0 表示正常</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>Message</td>
    <td>message</td>
    <td>错误信息</td>
    <td>string</td>
    <td></td>
  </tr>
  <tr>
    <td>Body</td>
    <td>body</td>
    <td>消息体，真正的消息通过 JSON 编码，加密后的字节数组</td>
    <td>[]byte</td>
    <td></td>
  </tr>
</table>

## Prometheus

假设本服务的 HTTP 地址设置为 `192.168.1.159:9527`，当需要为 Prometheus 提供监控数据时，只需要在 Prometheus 的配置中增加如下的 `scrape_config`:

```yaml
# A scrape configuration containing exactly one endpoint to scrape:
scrape_configs:
  # The job name is added as a label `job=<job_name>` to any timeseries scraped from this config.
  - job_name: "prometheus"

    # metrics_path defaults to '/metrics'
    # scheme defaults to 'http'.

    metrics_path: "/metrics/prometheus"
    static_configs:
      - targets: ["192.168.1.159:9527"]
```

## 参考文档

1. 本项目使用 IP2Location LITE 资料库于 <a href="https://lite.ip2location.com">IP地理定位</a>.
