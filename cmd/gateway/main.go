package main

import (
	"bufio"
	"context"
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

	centerHost := cfg.GetString("center_host", "127.0.0.1")
	centerPort := int(cfg.GetInt("center_port", 9100))
	loginHost := cfg.GetString("login_host", "127.0.0.1")
	loginPort := int(cfg.GetInt("login_port", 9200))

	common.Info("Starting %s on %s:%d (TCP) %s:%d (gRPC), center=%s:%d login=%s:%d",
		name, tcpHost, tcpPort, tcpHost, grpcPort, centerHost, centerPort, loginHost, loginPort)

	gw := gateway.NewGateway(name, tcpHost, tcpPort, grpcPort)
	gw.Init(redisHost, redisPort, centerHost, centerPort, loginHost, loginPort)

	// 注册到中心服 + 心跳 + 服务发现刷新 + 超时扫描
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gw.Start(ctx)

	// 启动推送 gRPC 服务（接收 game 服务的快照/帧/结果推送）
	if err := gw.StartGRPCServer(tcpHost, grpcPort); err != nil {
		common.Fatal("Failed to start push gRPC server: %v", err)
	}

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
		gw.Close()
		listener.Close()
		os.Exit(0)
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
	session := &gateway.Session{
		Conn:       conn,
		LastPingMs: common.NowMs(),
	}

	defer func() {
		conn.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		gw.OnClientDisconnect(cleanupCtx, session)
	}()

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

			// 消费这一帧（整帧长度 = 长度字段值 = 帧头 8 + body）
			totalLen := netproto.FrameHeaderSize + len(body)
			buf = buf[totalLen:]

			// 任何数据都视为活跃
			gw.HandleHeartbeat(session)

			// 心跳帧：原样回发
			if flags&netproto.FlagHeartbeat != 0 {
				gw.PongHeartbeat(session)
				continue
			}

			// 处理消息
			handleMessage(gw, session, msgID, flags, body)
		}
	}
}

func handleMessage(gw *gateway.Gateway, session *gateway.Session, msgID uint16, flags uint16, body []byte) {
	switch msgID {
	case netproto.MsgLoginReq:
		ctx, cancel := common.NewContextWithTimeout(5 * time.Second)
		defer cancel()
		gw.HandleLoginReq(ctx, session, body)

	case netproto.MsgRegisterReq:
		ctx, cancel := common.NewContextWithTimeout(5 * time.Second)
		defer cancel()
		gw.HandleRegisterReq(ctx, session, body)

	case netproto.MsgMatchReq:
		ctx, cancel := common.NewContextWithTimeout(15 * time.Second)
		defer cancel()
		gw.HandleMatchReq(ctx, session, body)

	case netproto.MsgOpInput:
		ctx, cancel := common.NewContextWithTimeout(1 * time.Second)
		defer cancel()
		gw.HandleOpInput(ctx, session, body)

	case netproto.MsgRankQuery:
		ctx, cancel := common.NewContextWithTimeout(5 * time.Second)
		defer cancel()
		gw.HandleRankQuery(ctx, session, body)

	default:
		common.Warn("Unknown message ID: %d", msgID)
	}
}
