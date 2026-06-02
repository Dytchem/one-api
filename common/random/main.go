package random

import (
	"crypto/rand"
	"encoding/binary"
	"math/big"

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

// cryptoRandInt 返回 [0, n) 范围的加密随机整数
func cryptoRandInt(n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	bi := big.NewInt(int64(n))
	r, err := rand.Int(rand.Reader, bi)
	if err != nil {
		return 0, err
	}
	return int(r.Int64()), nil
}

// readCharset 用 crypto/rand 填充 n 字节的字符集随机
// dyt-27: 替代原 math/rand 实现，确保 API key 不可预测
func readCharset(charset string, n int) (string, error) {
	cs := []byte(charset)
	csLen := len(cs)
	if csLen == 0 || n <= 0 {
		return "", nil
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		idx, err := cryptoRandInt(csLen)
		if err != nil {
			return "", err
		}
		out[i] = cs[idx]
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
