package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"DistributedDetectionNode/types"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/showwin/speedtest-go/speedtest"
)

var addr = flag.String("addr", "47.130.164.32:7800", "websocket service address")
var wallet = flag.String("wallet", "", "EVM wallet address (42 characters)")

const (
	writeWait = 10 * time.Second

	// 服务端 30s 内看不到 ping 就会断开，客户端心跳留足余量，同时放宽 pong 容错窗口。
	pongWait   = 25 * time.Second
	pingPeriod = 20 * time.Second

	maxMessageSize = 512

	configFile       = "config.json"
	onlineDelay      = 3 * time.Second
	machineInfoDelay = 3 * time.Second
	retryDelay       = 10 * time.Second
	reconnectDelay   = 5 * time.Second
	speedtestTimeout = 45 * time.Second
)

var errShutdown = errors.New("shutdown requested")

type envelope struct {
	t   int
	msg []byte
}

type systemInfo struct {
	CpuCores    int32  `json:"cpu_cores" bson:"cpu_cores,omitempty"`
	MemoryTotal int64  `json:"memory_total" bson:"memory_total,omitempty"`
	Hdd         int64  `json:"hdd" bson:"hdd,omitempty"`
	Bandwidth   int32  `json:"bandwidth" bson:"bandwidth,omitempty"`
	Wallet      string `json:"wallet" bson:"wallet,omitempty"`
}

type Config struct {
	MachineId  string `json:"machine_id"`
	PrivateKey string `json:"private_key"`
	Wallet     string `json:"wallet"`
}

func GenMachineId() (string, string, error) {
	privateKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate key pair: %v", err)
	}

	privateKeyHex := "0x" + hex.EncodeToString(crypto.FromECDSA(privateKey))

	publicKey := privateKey.PublicKey
	publicKeyBytes := crypto.FromECDSAPub(&publicKey)
	if len(publicKeyBytes) > 32 {
		publicKeyBytes = publicKeyBytes[1:33]
	}

	publicKeyHex := hex.EncodeToString(publicKeyBytes)

	return publicKeyHex, privateKeyHex, nil
}

func GetSystemStats(ctx context.Context) systemInfo {
	info := systemInfo{}

	if diskStat, err := disk.Usage("/"); err != nil {
		log.Printf("get disk usage failed: %v", err)
	} else {
		info.Hdd = int64(diskStat.Total / 1024 / 1024 / 1024)
	}

	if cpuCores, err := cpu.Counts(true); err != nil {
		log.Printf("get cpu cores failed: %v", err)
	} else {
		info.CpuCores = int32(cpuCores)
	}

	if memStat, err := mem.VirtualMemory(); err != nil {
		log.Printf("get memory stats failed: %v", err)
	} else {
		info.MemoryTotal = int64(memStat.Total / 1000 / 1000 / 1000)
	}

	if bandwidth, err := measureUploadSpeed(ctx); err != nil {
		log.Printf("measure upload speed failed: %v", err)
	} else {
		info.Bandwidth = bandwidth
	}

	return info
}

func measureUploadSpeed(ctx context.Context) (int32, error) {
	speedTestClient := speedtest.New()

	serverList, err := speedTestClient.FetchServerListContext(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch speedtest servers: %w", err)
	}

	targets, err := serverList.FindServer([]int{})
	if err != nil {
		return 0, fmt.Errorf("find speedtest server: %w", err)
	}

	var lastErr error
	for _, server := range targets {
		if err := server.UploadTestContext(ctx); err != nil {
			lastErr = fmt.Errorf("upload test on %s failed: %w", server.Host, err)
			continue
		}

		uploadSpeed := float64(server.ULSpeed*8) / 1e6
		if uploadSpeed <= 0 {
			lastErr = fmt.Errorf("upload speed invalid: %v", server.ULSpeed)
			continue
		}

		return int32(uploadSpeed), nil
	}

	if lastErr == nil {
		lastErr = errors.New("no speedtest server succeeded")
	}
	return 0, lastErr
}

func loadConfig() (*Config, error) {
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		log.Println("Config file does not exist, generating new config...")
		publicKeyHex, privateKeyHex, err := GenMachineId()
		if err != nil {
			return nil, err
		}

		config := &Config{
			MachineId:  publicKeyHex,
			PrivateKey: privateKeyHex,
			Wallet:     "",
		}

		configBytes, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return nil, err
		}

		if err := os.WriteFile(configFile, configBytes, 0644); err != nil {
			return nil, err
		}

		return config, nil
	}

	configBytes, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(configBytes, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func waitOrDone(done <-chan struct{}, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-done:
			return false
		default:
			return true
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-done:
		return false
	case <-timer.C:
		return true
	}
}

func enqueue(done <-chan struct{}, writeQueue chan<- envelope, msg envelope) bool {
	select {
	case <-done:
		return false
	case writeQueue <- msg:
		return true
	}
}

func runClient(config *Config, interrupt <-chan os.Signal) error {
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp4", addr)
		},
		HandshakeTimeout: 30 * time.Second,
	}

	u := url.URL{Scheme: "ws", Host: *addr, Path: "/websocket"}
	log.Printf("connecting to %s", u.String())

	c, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	defer c.Close()

	done := make(chan struct{})
	writeQueue := make(chan envelope, 16)
	readErrCh := make(chan error, 1)
	var reqID uint64
	var sendOnline func(time.Duration)
	var sendMachineInfo func(time.Duration)

	nextReqID := func() uint64 {
		return atomic.AddUint64(&reqID, 1) - 1
	}

	sendOnline = func(delay time.Duration) {
		if !waitOrDone(done, delay) {
			return
		}

		onlineReq := &types.WsOnlineRequest{
			MachineKey: types.MachineKey{
				MachineId:   config.MachineId,
				Project:     "DeepLink BandWidth",
				ContainerId: "",
			},
			StakingType: types.Free,
		}

		reqBody, err := json.Marshal(onlineReq)
		if err != nil {
			log.Printf("marshal online request body failed: %v", err)
			return
		}

		req := &types.WsRequest{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().UnixMilli(),
				Id:        nextReqID(),
				Type:      uint32(types.WsMtOnline),
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Body: reqBody,
		}

		reqBytes, err := json.Marshal(req)
		if err != nil {
			log.Printf("marshal online request failed: %v", err)
			return
		}

		if !enqueue(done, writeQueue, envelope{t: websocket.TextMessage, msg: reqBytes}) {
			log.Print("connection already closed, skip online request")
		}
	}

	sendMachineInfo = func(delay time.Duration) {
		if !waitOrDone(done, delay) {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), speedtestTimeout)
		systemInfo := GetSystemStats(ctx)
		cancel()

		machineInfo := &types.DeepLinkMachineInfoBandwidth{
			CpuCores:    systemInfo.CpuCores,
			MemoryTotal: systemInfo.MemoryTotal,
			Hdd:         systemInfo.Hdd,
			Bandwidth:   systemInfo.Bandwidth,
			Wallet:      config.Wallet,
		}

		if err := machineInfo.Validate(); err != nil {
			log.Printf("machine info invalid, retry later: %v", err)
			go sendMachineInfo(retryDelay)
			return
		}

		reqBody, err := json.Marshal(machineInfo)
		if err != nil {
			log.Printf("marshal machine info request body failed: %v", err)
			return
		}

		req := &types.WsRequest{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().UnixMilli(),
				Id:        nextReqID(),
				Type:      uint32(types.WsMtDeepLinkMachineInfoBW),
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Body: reqBody,
		}

		reqBytes, err := json.Marshal(req)
		if err != nil {
			log.Printf("marshal machine info request failed: %v", err)
			return
		}

		if !enqueue(done, writeQueue, envelope{t: websocket.TextMessage, msg: reqBytes}) {
			log.Print("connection already closed, skip machine info request")
		}
	}

	c.SetReadLimit(maxMessageSize)
	c.SetReadDeadline(time.Now().Add(pongWait))
	c.SetPongHandler(func(string) error {
		return c.SetReadDeadline(time.Now().Add(pongWait))
	})

	go func() {
		defer close(done)

		for {
			if err := c.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
				readErrCh <- fmt.Errorf("set read deadline failed: %w", err)
				return
			}

			_, message, err := c.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					readErrCh <- fmt.Errorf("read error: %w", err)
				} else {
					readErrCh <- fmt.Errorf("connection closed: %w", err)
				}
				return
			}

			log.Printf("recv: %s", message)

			response := &types.WsResponse{}
			if err := json.Unmarshal(message, response); err != nil {
				log.Printf("parse response failed: %v", err)
				continue
			}

			switch response.Type {
			case uint32(types.WsMtOnline):
				if response.Code == 0 {
					go sendMachineInfo(machineInfoDelay)
				} else {
					log.Printf("online failed %v %v", response.Code, response.Message)
					go sendOnline(retryDelay)
				}
			case uint32(types.WsMtDeepLinkMachineInfoBW):
				if response.Code == 0 {
					log.Printf("send machine info bandwidth success")
				} else {
					log.Printf("send machine info failed %v %v", response.Code, response.Message)
					go sendMachineInfo(retryDelay)
				}
			case uint32(types.WsMtNotify):
				notifyMessage := &types.WsNotifyMessage{}
				if err := json.Unmarshal(response.Body, notifyMessage); err != nil {
					log.Printf("parse notify response failed: %v", err)
					continue
				}
				if notifyMessage.Unregister.Message != "" {
					readErrCh <- errors.New("machine was unregistered by server")
					return
				}
			}
		}
	}()

	go sendOnline(onlineDelay)

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case err := <-readErrCh:
			return err
		case t := <-ticker.C:
			if err := c.WriteControl(websocket.PingMessage, []byte(t.String()), time.Now().Add(writeWait)); err != nil {
				return fmt.Errorf("ping websocket failed: %w", err)
			}
		case message, ok := <-writeQueue:
			if !ok {
				_ = c.WriteMessage(websocket.CloseMessage, []byte{})
				return nil
			}

			if err := c.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return fmt.Errorf("set write deadline failed: %w", err)
			}

			if message.t == websocket.CloseMessage {
				if err := c.WriteMessage(websocket.CloseMessage, message.msg); err != nil {
					return fmt.Errorf("write close message failed: %w", err)
				}
				return nil
			}

			if err := c.WriteMessage(message.t, message.msg); err != nil {
				return fmt.Errorf("write message failed: %w", err)
			}
		case <-interrupt:
			log.Println("interrupt")

			if err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
				log.Printf("write close failed: %v", err)
			}

			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return errShutdown
		}
	}
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if *wallet != "" {
		if len(*wallet) != 42 {
			log.Fatal("Error: EVM wallet address must be 42 characters long, including '0x' prefix")
		}

		config, err := loadConfig()
		if err != nil {
			log.Fatalf("Failed to load or create config: %v", err)
		}

		config.Wallet = *wallet

		cleanConfig := &Config{
			MachineId:  config.MachineId,
			PrivateKey: config.PrivateKey,
			Wallet:     config.Wallet,
		}

		configBytes, err := json.MarshalIndent(cleanConfig, "", "  ")
		if err != nil {
			log.Fatalf("Error marshaling updated config: %v", err)
		}
		if err := os.WriteFile(configFile, configBytes, 0644); err != nil {
			log.Fatalf("Error writing updated config file: %v", err)
		}

		fmt.Println("Wallet address has been set and saved to config.json")
		fmt.Println("Your machine id: ", config.MachineId)
		os.Exit(0)
	}

	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load or create config: %v", err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	for {
		err := runClient(config, interrupt)
		if err == nil {
			return
		}
		if errors.Is(err, errShutdown) {
			return
		}

		log.Printf("client exited, will reconnect in %s: %v", reconnectDelay, err)

		select {
		case <-interrupt:
			return
		case <-time.After(reconnectDelay):
		}
	}
}
