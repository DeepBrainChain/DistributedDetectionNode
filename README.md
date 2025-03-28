# DistributedDetectionNode

Distributed Detection Node

## Design plan

1. Provide HTTP/Websocket services, and use whether the Websocket is connected as the basis for judging whether it is online.
2. Use MongoDB database to store the indicator data pushed by the client and support horizontally scalable cluster services.
3. Provide some interfaces to provide data for Prometheus.
4. Provide some management interfaces or functions, such as data cleaning, persistence, etc.

- [MongoDB time series](https://www.mongodb.com/zh-cn/products/capabilities/time-series)
- [MongoDB time series user document](https://www.mongodb.com/zh-cn/docs/manual/core/timeseries-collections/)
- [MongoDB Go Driver time series collection](https://www.mongodb.com/zh-cn/docs/drivers/go/current/fundamentals/time-series/)
- [Prometheus Pushgateway](https://github.com/prometheus/pushgateway)

Note: MongoDB 5.0 version is too low, the time series function is incomplete, and there are bugs. You need to use the latest version 7.0 or above.

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

Create a JSON configuration file, as shown below:

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

Use the command `ddn -config ./config.json` to run.

The program will start a WebSocket service, which can be connected using `ws://192.168.1.159:9521/websocket`.

## WebSocket

WebSocket sets up a heartbeat service, that is, the client sends a ping message and the service replies with a pong message.
If there is no ping message within 30 seconds, the connection will be disconnected by the server.
Please send ping messages in time, which is both a heartbeat and can ensure the stability and reliability of long connections.

WebSocket messages use UTF-8 text format, mainly in JSON format. For specific examples, please see [Test Case](./ws/ws_test.go)

The request message sent by the client to the server mainly consists of two parts: Header and Body.

<table>
  <tr>
    <td></td>
    <td>Field</td>
    <td>Description</td>
    <td>Type</td>
    <td>Remarks</td>
  </tr>
  <tr>
    <td rowspan="6">Header</td>
    <td>version</td>
    <td>Protocol version, temporarily use 0</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>timestamp</td>
    <td>Timestamp</td>
    <td>int64</td>
    <td></td>
  </tr>
  <tr>
    <td>id</td>
    <td>Message sequence number, a pair of request and response sequence numbers are the same</td>
    <td>uint64</td>
    <td></td>
  </tr>
  <tr>
    <td>type</td>
    <td>Message body type, 0 - reserved, 1 - online, 2 - machine information</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>pub_key</td>
    <td>Public key, verify the security and integrity of the message, not required for the time being</td>
    <td>[]byte</td>
    <td></td>
  </tr>
  <tr>
    <td>sign</td>
    <td>Signature, verify the security and integrity of the message, not required for the time being</td>
    <td>[]byte</td>
    <td></td>
  </tr>
  <tr>
    <td>Body</td>
    <td>body</td>
    <td>Message body, the real message is encoded in JSON, the encrypted byte array</td>
    <td>[]byte</td>
    <td></td>
  </tr>
</table>

The message body currently has the following types:
- 0 - meaningless.
- 1 - Online, indicating that the WebSocket connection belongs to that device or node.
```json
{
  "machine_id": "123456789",
  "project": "deeplink",
  "staking_type": 0
}
```
- 2 - DeepLink short-term rental equipment information, regularly send graphics card and other machine information.
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
- 3 - Notify message.
```json
{
  "unregister": {
    "message": "machine unregistered, notify from node server"
  }
}
```
- 4 - DeepLink bandwidth mining equipment information.
```json
{
  "cpu_cores": 1,
  "memory_total": 2,
  "hdd": 50,
  "bandwidth": 10,
  "wallet": "xxxxxx"
}
```

The format structure of the response message body returned by the server to the client is similar, except that there are two more fields, Code and Message, than the request.

<table>
  <tr>
    <td></td>
    <td>Field</td>
    <td>Description</td>
    <td>Type</td>
    <td>Remarks</td>
  </tr>
  <tr>
    <td rowspan="6">Header</td>
    <td>version</td>
    <td>Protocol version, temporarily use 0</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>timestamp</td>
    <td>Timestamp</td>
    <td>int64</td>
    <td></td>
  </tr>
  <tr>
    <td>id</td>
    <td>Message sequence number, a pair of request and response sequence numbers are the same</td>
    <td>uint64</td>
    <td></td>
  </tr>
  <tr>
    <td>type</td>
    <td>Message body type, 0 - reserved, 1 - online, 2 - machine information</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>pub_key</td>
    <td>Public key, verify the security and integrity of the message, temporarily not required</td>
    <td>[]byte</td>
    <td></td>
  </tr>
  <tr>
    <td>sign</td>
    <td>Signature, verify the security and integrity of the message, temporarily not required</td>
    <td>[]byte</td>
    <td></td>
  </tr>
  <tr>
    <td>Code</td>
    <td>code</td>
    <td>Error code, 0 means normal</td>
    <td>uint32</td>
    <td></td>
  </tr>
  <tr>
    <td>Message</td>
    <td>message</td>
    <td>Error message</td>
    <td>string</td>
    <td></td>
  </tr>
  <tr>
    <td>Body</td>
    <td>body</td>
    <td>Message body, the real message is encoded through JSON, encrypted byte array</td>
    <td>[]byte</td>
    <td></td>
  </tr>
</table>

## Prometheus

Assuming that the HTTP address of this service is set to `192.168.1.159:9527`, when you need to provide monitoring data for Prometheus, you only need to add the following `scrape_config` to the Prometheus configuration:

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

## References

1. Product uses the IP2Location LITE database for <a href="https://lite.ip2location.com">IP geolocation</a>.
