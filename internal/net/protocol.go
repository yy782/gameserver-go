package net

import (
	"encoding/binary"
	"errors"
)

// 消息标志位定义
const (
	FlagReq       = 0x0001 // 请求
	FlagRsp       = 0x0002 // 应答
	FlagPush      = 0x0004 // 推送
	FlagHeartbeat = 0x0008 // 心跳
)

// 消息ID定义
const (
	MsgLoginReq    = 1  // 登录请求
	MsgLoginRsp    = 2  // 登录应答
	MsgMatchReq    = 3  // 匹配请求
	MsgMatchRsp    = 4  // 匹配应答
	MsgOpInput     = 5  // 玩家操作
	MsgSnapshot    = 6  // 状态快照
	MsgResult      = 7  // 对局结果
	MsgRankQuery   = 8  // 排行榜查询
	MsgRankRsp     = 9  // 排行榜应答
	MsgRegisterReq = 10 // 注册请求
	MsgRegisterRsp = 11 // 注册应答
	MsgFrameData   = 12 // 帧数据
	MsgKick        = 13 // 踢线通知
)

// 协议常量
const (
	MaxFrameSize    = 1024 * 1024 // 单帧最大长度
	FrameHeaderSize = 8           // 帧头长度
)

var (
	ErrFrameTooLarge  = errors.New("帧太大")
	ErrBufferTooSmall = errors.New("缓冲区太小")
)

// EncodeFrame 组帧为二进制格式（与 C++ net/protocol.cpp EncodeFrame 一致）
// 格式：[4B 长度] [2B msg_id] [2B flags] [protobuf payload]
// 长度字段 = kFrameHeaderSize + len(body)，即整帧长度（含长度字段自身）
func EncodeFrame(msgID uint16, flags uint16, body []byte) []byte {
	frameLen := uint32(FrameHeaderSize + len(body))
	frame := make([]byte, frameLen)

	binary.BigEndian.PutUint32(frame[0:4], frameLen)
	binary.BigEndian.PutUint16(frame[4:6], msgID)
	binary.BigEndian.PutUint16(frame[6:8], flags)
	copy(frame[8:], body)

	return frame
}

// TryDecodeFrame 从缓冲区解析一帧（不消费缓冲，由调用方按整帧长度切除）
// 成功返回 true 并填充 msg_id/flags/body
// 数据不足返回 false（等待更多数据）
func TryDecodeFrame(buf []byte, msgID *uint16, flags *uint16, body *[]byte) (bool, error) {
	if len(buf) < FrameHeaderSize {
		return false, nil
	}

	frameLen := binary.BigEndian.Uint32(buf[0:4])
	if frameLen < FrameHeaderSize {
		// 非法长度：协议错乱
		return false, ErrFrameTooLarge
	}
	if frameLen > MaxFrameSize {
		return false, ErrFrameTooLarge
	}

	if len(buf) < int(frameLen) {
		return false, nil // 整包未到齐
	}

	*msgID = binary.BigEndian.Uint16(buf[4:6])
	*flags = binary.BigEndian.Uint16(buf[6:8])
	*body = buf[FrameHeaderSize:int(frameLen)]

	return true, nil
}
