package wechat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// WeChat's encrypted message-push protocol uses a 32-byte PKCS#7 padding
// block, independently of AES-CBC's 16-byte cipher block size.
const wechatPKCS7BlockSize = 32

func VerifyURLSignature(token, signature, timestamp, nonce string) bool {
	expected := wechatSignature(token, timestamp, nonce)
	return expected != "" && strings.EqualFold(strings.TrimSpace(signature), expected)
}

func VerifyMessageSignature(token, msgSignature, timestamp, nonce, encrypted string) bool {
	expected := wechatSignature(token, timestamp, nonce, encrypted)
	return expected != "" && strings.EqualFold(strings.TrimSpace(msgSignature), expected)
}

func DecryptMessageJSON(encodingAESKey, appID, encrypted string) ([]byte, error) {
	aesKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodingAESKey) + "=")
	if err != nil {
		return nil, fmt.Errorf("wechat message push decode aes key failed: %w", err)
	}
	if len(aesKey) != 32 {
		return nil, fmt.Errorf("wechat message push invalid aes key length: %d", len(aesKey))
	}

	cipherText, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return nil, fmt.Errorf("wechat message push decode encrypt payload failed: %w", err)
	}
	if len(cipherText) == 0 || len(cipherText)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("wechat message push invalid encrypted payload length")
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("wechat message push init cipher failed: %w", err)
	}

	plainText := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, aesKey[:aes.BlockSize]).CryptBlocks(plainText, cipherText)

	plainText, err = pkcs7Unpad(plainText, wechatPKCS7BlockSize)
	if err != nil {
		return nil, err
	}
	if len(plainText) < 20 {
		return nil, fmt.Errorf("wechat message push decrypted payload is too short")
	}

	msgLen := binary.BigEndian.Uint32(plainText[16:20])
	if len(plainText) < 20+int(msgLen) {
		return nil, fmt.Errorf("wechat message push decrypted message is truncated")
	}

	message := plainText[20 : 20+int(msgLen)]
	messageAppID := string(plainText[20+int(msgLen):])
	if strings.TrimSpace(appID) != "" && strings.TrimSpace(messageAppID) != strings.TrimSpace(appID) {
		return nil, fmt.Errorf("wechat message push appid mismatch")
	}

	return bytes.TrimSpace(message), nil
}

func wechatSignature(parts ...string) string {
	values := make([]string, 0, len(parts))
	for _, item := range parts {
		values = append(values, strings.TrimSpace(item))
	}
	sort.Strings(values)
	sum := sha1.Sum([]byte(strings.Join(values, "")))
	return hex.EncodeToString(sum[:])
}

func pkcs7Unpad(src []byte, blockSize int) ([]byte, error) {
	if len(src) == 0 || len(src)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("wechat message push invalid pkcs7 payload")
	}
	padding := int(src[len(src)-1])
	if padding == 0 || padding > blockSize || padding > len(src) {
		return nil, fmt.Errorf("wechat message push invalid pkcs7 padding")
	}
	for _, item := range src[len(src)-padding:] {
		if int(item) != padding {
			return nil, fmt.Errorf("wechat message push invalid pkcs7 padding")
		}
	}
	return src[:len(src)-padding], nil
}
