package random

import (
	"crypto/rand"
	"encoding/binary"

	"github.com/google/uuid"
	"strings"
)

func GetUUID() string {
	code := uuid.New().String()
	code = strings.Replace(code, "-", "", -1)
	return code
}

const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const keyNumbers = "0123456789"

// readCharset 用 crypto/rand 填充 n 字节的字符集随机
// dyt-27: 替代原 math/rand 实现，确保 API key 不可预测
// dyt-100: 拒绝采样批量读取 —— 一次 rand.Read 批量取字节，丢弃超出有效范围的值保证均匀性，
// 避免逐字符 cryptoRandInt（每个字符一次 crypto/rand 系统调用 + big.Int 分配）
func readCharset(charset string, n int) (string, error) {
	cs := []byte(charset)
	csLen := len(cs)
	if csLen == 0 || n <= 0 {
		return "", nil
	}
	out := make([]byte, n)
	// 有效范围 = 256/csLen 的整数倍（取整），落在范围内的字节才可用，保证均匀
	limit := (256 / csLen) * csLen
	filled := 0
	for filled < n {
		buf := make([]byte, n-filled)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if filled >= n {
				break
			}
			if int(b) < limit {
				out[filled] = cs[int(b)%csLen]
				filled++
			}
		}
	}
	return string(out), nil
}

// GenerateKey 完全等价旧实现：48 字符，前 16 字符真随机 + 后 32 字符 UUID 转大写小写交替
// 行为兼容：旧 token 仍然有效
func GenerateKey() string {
	prefix, err := readCharset(keyChars, 16)
	if err != nil {
		// 极端情况：crypto/rand 失败时降级为 UUID 段
		prefix = GetUUID()[:16]
	}
	uuid_ := GetUUID()
	key := make([]byte, 48)
	copy(key[:16], prefix)
	for i := 0; i < 32; i++ {
		c := uuid_[i]
		if i%2 == 0 && c >= 'a' && c <= 'z' {
			c = c - 'a' + 'A'
		}
		key[i+16] = c
	}
	return string(key)
}

func GetRandomString(length int) string {
	s, err := readCharset(keyChars, length)
	if err != nil {
		return GetUUID()[:min(length, 32)]
	}
	return s
}

func GetRandomNumberString(length int) string {
	s, err := readCharset(keyNumbers, length)
	if err != nil {
		return GetUUID()[:min(length, 32)]
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// RandRange returns a random number between min and max (max is not included)
// dyt-27: 升级为 crypto/rand，避免渠道选择可被预测
func RandRange(min, max int) int {
	if max <= min {
		return min
	}
	span := max - min
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return min // 失败回退
	}
	n := binary.BigEndian.Uint64(b[:])
	return min + int(n%uint64(span))
}
