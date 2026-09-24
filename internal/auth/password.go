package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
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

// randomCode 从字母表等概率取样（拒绝取模偏差：取值空间与字母表长度不整除时，
// 简单取模会让前几个字符更常见，直接削弱随机码熵）。
func randomCode(alphabet string, length int) (string, error) {
	max := big.NewInt(int64(len(alphabet)))
	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}

// GenerateSuperCode 生成 10 位超级验证码明文。
func GenerateSuperCode() (string, error) {
	return randomCode(superCodeAlphabet, superCodeLen)
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

// RandomCode 暴露等概率随机码生成（重置码等场景复用，避免取模偏差）。
func RandomCode(alphabet string, length int) (string, error) {
	return randomCode(alphabet, length)
}

// dummyHash 用于「用户不存在」时的等时校验：预置一份 argon2id 哈希，
// 让不存在用户的登录耗时与密码错误的用户一致，避免用响应耗时枚举账号。
var dummyHash string

func init() {
	if h, err := HashPassword("onecloud-panel-timing-equalizer"); err == nil {
		dummyHash = h
	}
}

// DummyVerify 对不存在的用户执行一次等价的 argon2id 校验（结果恒为 false）。
func DummyVerify(password string) {
	if dummyHash == "" {
		return
	}
	_, _ = VerifyPassword(password, dummyHash)
}
