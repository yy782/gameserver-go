package common

import (
	"math/rand"
	"strconv"
	"strings"
)

// 以下哈希实现与 C++ src/common/hash.cpp 完全一致：
// 以 4 个固定种子分别做 FNV-1a，每个种子输出 16 个 hex 字符，共 64 位 hex。
const (
	fnvOffset = uint64(14695981039346656037)
	fnvPrime  = uint64(1099511628211)
)

var hashSeeds = [4]uint64{
	0x9e3779b97f4a7c15,
	0xbf58476d1ce4e5b9,
	0x94d049bb133111eb,
	0xbf8ea6f9a9a5a6b3,
}

func fnv1a(input string, seed uint64) uint64 {
	h := fnvOffset ^ seed
	for i := 0; i < len(input); i++ {
		h ^= uint64(input[i])
		h *= fnvPrime
	}
	return h
}

// HashHex 加盐哈希：输入任意字符串，输出 64 位小写 hex（同 C++ HashHex）
func HashHex(input string) string {
	const hexChars = "0123456789abcdef"
	var sb strings.Builder
	sb.Grow(64)
	for _, seed := range hashSeeds {
		v := fnv1a(input, seed)
		for shift := uint(60); ; shift -= 4 {
			sb.WriteByte(hexChars[(v>>shift)&0xF])
			if shift == 0 {
				break
			}
		}
	}
	return sb.String()
}

// HashWithSalt 加盐哈希：规则 = HashHex(salt + input)，与 C++ login 一致
func HashWithSalt(input, salt string) string {
	return HashHex(salt + input)
}

// VerifyHash 校验哈希值
func VerifyHash(input, hash string) bool {
	return HashHex(input) == hash
}

// GenerateSalt 生成 6 位随机盐值（同 C++ RandomInt(100000, 999999)）
func GenerateSalt() string {
	return strconv.Itoa(RandomInt(100000, 999999))
}

// RandomInt 返回 [min, max] 区间随机整数
func RandomInt(min, max int) int {
	if max <= min {
		return min
	}
	return min + rand.Intn(max-min+1)
}
