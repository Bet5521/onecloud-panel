package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemoryKiB = 64 * 1024 // 64 MiB
	argonTime      = 1
	argonParallel  = 2
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// HashPassword 返回 argon2id PHC 格式哈希。
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonParallel, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonParallel,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword 校验密码与 PHC 哈希是否匹配。
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("非 argon2id 哈希格式")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, err
	}
	if version != argon2.Version {
		return false, errors.New("argon2 版本不匹配")
	}
	var memory uint32
	var time uint32
	var parallel uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &parallel); err != nil {
		return false, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, parallel, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// superCodeAlphabet 去除易混字符 I/L/O/0/1。
const superCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const superCodeLen = 10

// GenerateSuperCode 生成 10 位超级验证码明文。
func GenerateSuperCode() (string, error) {
	b := make([]byte, superCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, superCodeLen)
	for i, c := range b {
		out[i] = superCodeAlphabet[int(c)%len(superCodeAlphabet)]
	}
	return string(out), nil
}

// HashSuperCode 对超级验证码做 SHA-256 哈希（hex）。
func HashSuperCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// VerifySuperCode 常量时间比较超级验证码与哈希。
func VerifySuperCode(code, hash string) bool {
	if hash == "" {
		return false
	}
	got := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	want, err := hex.DecodeString(hash)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got[:], want) == 1
}
