package net

import "testing"

// 镜像 C++ tests/unit_tests/test_protocol.cpp：
// 帧总长 = kFrameHeaderSize(8) + body；长度字段 = 帧总长；消息/标志/载荷往返一致。
func TestFrameRoundTrip(t *testing.T) {
	body := []byte("hello")
	frame := EncodeFrame(MsgLoginReq, FlagReq|FlagHeartbeat, body)

	if len(frame) != FrameHeaderSize+len(body) {
		t.Fatalf("frame size=%d, want %d", len(frame), FrameHeaderSize+len(body))
	}

	buf := append([]byte{}, frame...)
	var msgID, flags uint16
	var decoded []byte
	ok, err := TryDecodeFrame(buf, &msgID, &flags, &decoded)
	if err != nil || !ok {
		t.Fatalf("TryDecodeFrame failed ok=%v err=%v", ok, err)
	}
	if msgID != MsgLoginReq {
		t.Fatalf("msg_id=%d, want %d", msgID, MsgLoginReq)
	}
	if flags != FlagReq|FlagHeartbeat {
		t.Fatalf("flags=%#x, want %#x", flags, FlagReq|FlagHeartbeat)
	}
	if string(decoded) != string(body) {
		t.Fatalf("body=%q, want %q", decoded, body)
	}
}

// 数据不足（半包）时返回 false，等待更多数据
func TestTryDecodeFrameIncomplete(t *testing.T) {
	frame := EncodeFrame(MsgMatchReq, FlagReq, []byte("abc"))
	var msgID, flags uint16
	var body []byte
	if ok, _ := TryDecodeFrame(frame[:FrameHeaderSize], &msgID, &flags, &body); ok {
		t.Fatal("expected false for incomplete frame")
	}
	if ok, _ := TryDecodeFrame(frame[:FrameHeaderSize-1], &msgID, &flags, &body); ok {
		t.Fatal("expected false for empty buffer")
	}
}

// 长度字段非法（小于帧头）视为协议错误
func TestTryDecodeFrameBadLength(t *testing.T) {
	frame := make([]byte, 12)
	if ok, err := TryDecodeFrame(frame, new(uint16), new(uint16), new([]byte)); ok || err == nil {
		t.Fatalf("expected error for bad frame length, got ok=%v err=%v", ok, err)
	}
}
