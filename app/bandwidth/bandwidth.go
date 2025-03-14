package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"DistributedDetectionNode/types"
)

var addr = flag.String("addr", "localhost:8080", "websocket service address")

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 10 * time.Second // 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

type envelope struct {
	t   int
	msg []byte
}

func main() {
	flag.Parse()
	log.SetFlags(0)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)

	u := url.URL{Scheme: "ws", Host: *addr, Path: "/websocket"}
	log.Printf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	done := make(chan struct{})
	writeQueue := make(chan envelope)
	var reqId uint64 = 0

	// send machine info using go sendOnline(time.Second)
	sendOnline := func(delay time.Duration) {
		time.Sleep(delay)
		onlineReq := &types.WsOnlineRequest{
			MachineKey: types.MachineKey{
				MachineId:   "123456789",
				Project:     "deeplink",
				ContainerId: "",
			},
			StakingType: types.ShortTerm,
		}
		reqBody, err := json.Marshal(onlineReq)
		if err != nil {
			log.Fatalf("marshal online request body failed: %v", err)
		}
		req := &types.WsRequest{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().UnixMilli(),
				Id:        reqId,
				Type:      uint32(types.WsMtOnline),
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Body: reqBody,
		}
		reqBytes, err := json.Marshal(req)
		if err != nil {
			log.Fatalf("marshal online request failed: %v", err)
		}
		select {
		case <-done:
			log.Print("connection already closed")
		default:
			writeQueue <- envelope{t: websocket.TextMessage, msg: reqBytes}
			reqId++
		}
	}

	// send machine info using go sendMachineInfo(time.Second)
	sendMachineInfo := func(delay time.Duration) {
		time.Sleep(delay)
		machineInfo := &types.DeepLinkMachineInfoBandwidth{
			CpuCores:    1,
			MemoryTotal: 2,
			Hdd:         50,
			Bandwidth:   10,
			Wallet:      "xxxxxx",
		}
		reqBody, err := json.Marshal(machineInfo)
		if err != nil {
			log.Fatalf("marshal machine info request body failed: %v", err)
		}
		req2 := &types.WsRequest{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().UnixMilli(),
				Id:        reqId,
				Type:      uint32(types.WsMtDeepLinkMachineInfoST),
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Body: reqBody,
		}
		reqBytes, err := json.Marshal(req2)
		if err != nil {
			log.Fatalf("marshal machine info request failed: %v", err)
		}
		select {
		case <-done:
			log.Print("connection already closed")
		default:
			writeQueue <- envelope{t: websocket.TextMessage, msg: reqBytes}
			reqId++
		}
	}

	// read websocket connection
	go func() {
		defer close(done)
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
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
					go sendMachineInfo(3 * time.Second)
				} else {
					log.Printf("online failed %v %v", response.Code, response.Message)
					go sendOnline(10 * time.Second)
				}
			case uint32(types.WsMtDeepLinkMachineInfoBW):
				if response.Code == 0 {
					log.Printf("send machine info bandwidth success")
				} else {
					log.Printf("online failed %v %v", response.Code, response.Message)
					go sendMachineInfo(10 * time.Second)
				}
			case uint32(types.WsMtNotify):
				notifyMessage := &types.WsNotifyMessage{}
				if err := json.Unmarshal(response.Body, notifyMessage); err != nil {
					log.Printf("parse notify response failed: %v", err)
				} else {
					if notifyMessage.Unregister.Message != "" {
						log.Printf("machine was unregistered by server")
						return
					}
				}
			}
		}
	}()

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	select {
	case <-done:
		return
	case t := <-ticker.C:
		if err := c.WriteControl(websocket.PingMessage, []byte(t.String()), time.Now().Add(9*time.Second)); err != nil {
			log.Printf("ping websocket failed: %v", err)
			return
		}
	case message, ok := <-writeQueue:
		c.SetWriteDeadline(time.Now().Add(writeWait))
		if !ok {
			// The hub closed the channel.
			c.WriteMessage(websocket.CloseMessage, []byte{})
			return
		}
		if message.t == websocket.CloseMessage {
			c.WriteMessage(websocket.CloseMessage, message.msg)
			return
		}
		c.WriteMessage(message.t, message.msg)
	case <-interrupt:
		log.Println("interrupt")

		// Cleanly close the connection by sending a close message and then
		// waiting (with timeout) for the server to close the connection.
		err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			log.Println("write close:", err)
			return
		}
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		return
	}

	log.Println("exiting")
}
