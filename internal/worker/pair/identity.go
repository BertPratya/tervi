package pair

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// appID is tervi's application ID for deriving the computer's identity from
// /etc/machine-id. Never change it: every paired computer would look new to
// the server.
var appID = [16]byte{0xca, 0x6e, 0x70, 0xbc, 0x29, 0xab, 0x4e, 0xd1,
	0xbc, 0x52, 0x60, 0x07, 0x94, 0xcb, 0xb1, 0x22}

const defaultMachineIDPath = "/etc/machine-id"

func machineIdentity(path string) (string, bool) {
	if path == "" {
		path = defaultMachineIDPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if len(data) == 33 && data[32] == '\n' {
		data = data[:32]
	}
	if len(data) != 32 {
		return "", false
	}
	key := make([]byte, 16)
	if _, err := hex.Decode(key, data); err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(appID[:])
	return hex.EncodeToString(mac.Sum(nil)), true
}
