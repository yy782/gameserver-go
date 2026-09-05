package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// HashHex 使用 SHA256 哈希
func HashHex(input string) string {
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

// VerifyHash 校验哈希值
func VerifyHash(input, hash string) bool {
	return HashHex(input) == hash
}

// HashWithSalt 使用盐值进行哈希
func HashWithSalt(input, salt string) string {
	return HashHex(input + salt)
}

// GenerateSalt 生成盐值
func GenerateSalt() string {
	return fmt.Sprintf("%d", NowMs())
}