package center

// 中心服服务注册表单元测试：注册/覆盖/心跳/超时摘除（纯内存逻辑，
// NewCenter 创建的 Redis 客户端为惰性连接，测试中不发起网络请求）。
import (
	"context"
	"testing"

	"gameserver/api/pb"
	"gameserver/internal/common"
)

func TestRegisterAndHeartbeat(t *testing.T) {
	c := NewCenter("127.0.0.1", 0)
	ctx := context.Background()

	rsp, err := c.RegisterService(ctx, &pb.RegReq{
		ServiceName: "game-1", Host: "10.0.0.1", Port: 9001, Kind: "game",
	})
	if err != nil || !rsp.Ok {
		t.Fatalf("注册失败: %v %v", rsp, err)
	}
	if _, ok := c.services["game-1"]; !ok {
		t.Fatal("注册后应在注册表中")
	}

	// 同名单服务重复注册 = 覆盖并刷新心跳
	if _, err := c.RegisterService(ctx, &pb.RegReq{
		ServiceName: "game-1", Host: "10.0.0.9", Port: 9999, Kind: "game",
	}); err != nil {
		t.Fatalf("重复注册失败: %v", err)
	}
	e := c.services["game-1"]
	if e.host != "10.0.0.9" || e.port != 9999 {
		t.Fatalf("重复注册应覆盖地址, got %s:%d", e.host, e.port)
	}

	// 心跳刷新 lastHB
	e.lastHB = 1
	if _, err := c.Heartbeat(ctx, &pb.HeartbeatReq{ServiceName: "game-1"}); err != nil {
		t.Fatalf("心跳失败: %v", err)
	}
	if e.lastHB == 1 {
		t.Fatal("心跳后 lastHB 应被刷新")
	}

	// 未注册服务心跳：no-op，不新增记录
	if _, err := c.Heartbeat(ctx, &pb.HeartbeatReq{ServiceName: "ghost"}); err != nil {
		t.Fatalf("未知服务心跳: %v", err)
	}
	if _, ok := c.services["ghost"]; ok {
		t.Fatal("未注册服务心跳不应新增记录")
	}
}

func TestGetServiceListIncludesLive(t *testing.T) {
	c := NewCenter("127.0.0.1", 0)
	ctx := context.Background()
	_, _ = c.RegisterService(ctx, &pb.RegReq{
		ServiceName: "login-1", Host: "h", Port: 8000, Kind: "login",
	})
	list, err := c.GetServiceList(ctx, &pb.Empty{})
	if err != nil || len(list.Services) != 1 {
		t.Fatalf("服务列表异常: %+v err=%v", list, err)
	}
	if list.Services[0].ServiceName != "login-1" {
		t.Fatalf("服务名不匹配: %s", list.Services[0].ServiceName)
	}
}

func TestGetServiceListExpiresStale(t *testing.T) {
	c := NewCenter("127.0.0.1", 0)
	ctx := context.Background()
	_, _ = c.RegisterService(ctx, &pb.RegReq{
		ServiceName: "rank-1", Host: "h", Port: 8002, Kind: "rank",
	})
	// 把心跳时间拨到超时阈值之前，GetServiceList 应将其摘除
	c.services["rank-1"].lastHB = common.NowMs() - heartbeatTimeoutMs - 1
	list, _ := c.GetServiceList(ctx, &pb.Empty{})
	if len(list.Services) != 0 {
		t.Fatalf("心跳超时的服务应被摘除, got %+v", list.Services)
	}
	if _, ok := c.services["rank-1"]; ok {
		t.Fatal("超时服务应从注册表删除")
	}
}
