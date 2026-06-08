package copilot

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
)

func solveHashcash(param string) string {
	parts := strings.SplitN(param, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	prefix := parts[0]
	difficulty, err := strconv.Atoi(parts[1])
	if err != nil {
		difficulty = 1
	}
	for nonce := 0; ; nonce++ {
		data := fmt.Sprintf("%s%d", prefix, nonce)
		hash := sha256.Sum256([]byte(data))
		if hasLeadingZeroBits(hash[:], difficulty) {
			return strconv.Itoa(nonce)
		}
	}
}

func hasLeadingZeroBits(hash []byte, n int) bool {
	fullBytes := n / 8
	remainingBits := n % 8

	for i := 0; i < fullBytes && i < len(hash); i++ {
		if hash[i] != 0 {
			return false
		}
	}
	if remainingBits > 0 && fullBytes < len(hash) {
		mask := byte(0xFF << (8 - remainingBits))
		return hash[fullBytes]&mask == 0
	}
	return true
}
