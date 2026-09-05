package main

import (
	"bufio"
	"flag"
	"fmt"
	"gameserver/internal/common"
	"gameserver/internal/gateway"
	netproto "gameserver/internal/net"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "../../../config/gateway.json", "config file path")
	flag.Parse()

	cfg := common.NewConfig()
	if err := cfg.LoadFile(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	name := cfg.GetString("name", "gateway")
	tcpHost := cfg.GetString("host", "0.0.0.0")
	tcpPort := int(cfg.GetInt("tcp_port", 8000))
	grpcPort := int(cfg.GetInt("grpc_port", 9300))
	redisHost := cfg.GetString("redis_host", "127.0.0.1")
	redisPort := int(cfg.GetInt("redis_port", 6379))

	common.Info("Starting %s on %s:%d (TCP) %s:%d (gRPC)", name, tcpHost, tcpPort, tcpHost, grpcPort)

	gw := gateway.NewGateway(name, tcpPort, grpcPort)
	gw.Init(redisHost, redisPort)

	// 启动 TCP 服务器
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", tcpHost, tcpPort))
	if err != nil {
		common.Fatal("Failed to listen TCP: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				common.Error("Accept error: %v", err)
				return
			}

			go handleClientConnection(gw, conn.(io.ReadWriteCloser))
		}
	}()

	// 监听退出信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		common.Info("Shutting down %s", name)
		listener.Close()
	}()

	// 读取输入防止退出
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			reader.ReadString('\n')
		}
	}()

	common.Info("%s started", name)
	select {}
}

func handleClientConnection(gw *gateway.Gateway, conn io.ReadWriteCloser) {
	defer conn.Close()

	session := &gateway.Session{
		Conn:       conn,
		LastPingMs: common.NowMs(),
	}

	buf := make([]byte, 0, 4096)
	readBuf := make([]byte, 4096)

	for {
		n, err := conn.Read(readBuf)
		if err != nil {
			if err != io.EOF {
				common.Error("Read error: %v", err)
			}
			return
		}

		buf = append(buf, readBuf[:n]...)

		// 尝试解析帧
		for len(buf) > 0 {
			msgID := uint16(0)
			flags := uint16(0)
			body := []byte(nil)

			ok, err := netproto.TryDecodeFrame(buf, &msgID, &flags, &body)
			if err != nil {
				common.Error("Decode frame error: %v", err)
				return
			}

			if !ok {
				break
			}

			// 消费这一帧
			totalLen := 4 + 8 + len(body)
			buf = buf[totalLen:]

			// 处理消息
			handleMessage(gw, session, msgID, flags, body)
		}
	}
}

func handleMessage(gw *gateway.Gateway, session *gateway.Session, msgID uint16, flags uint16, body []byte) {
	switch msgID {
	case netproto.MsgLoginReq:
		ctx := common.NewContextWithTimeout(5 * time.Second)
		gw.HandleLoginReq(ctx, session, body)

	case netproto.MsgMatchReq:
		ctx := common.NewContextWithTimeout(5 * time.Second)
		gw.HandleMatchReq(ctx, session, body)

	case netproto.MsgOpInput:
		ctx := common.NewContextWithTimeout(1 * time.Second)
		gw.HandleOpInput(ctx, session, body)

	case netproto.MsgRankQuery:
		ctx := common.NewContextWithTimeout(5 * time.Second)
		gw.HandleRankQuery(ctx, session, body)

	default:
		common.Warn("Unknown message ID: %d", msgID)
	}
}