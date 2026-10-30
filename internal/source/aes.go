package source

import (
	"crypto/aes"
	"crypto/cipher"
)

// aesEncrypt implements the parts of lx's utils.crypto.aesEncrypt the
// sources use: aesEncrypt(data, key, iv, mode) with mode "ecb" or "cbc",
// PKCS#7 padding, returned as raw ciphertext (the caller base64-encodes).
func aesEncrypt(data, key, iv []byte, mode string) ([]byte, error) {
	// lx sources use 128-bit keys and 16-byte IVs; pad to the block size.
	k := padKey(key, aes.BlockSize)
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	plain := pkcs7(data, block.BlockSize())
	switch mode {
	case "ecb":
		out := make([]byte, len(plain))
		for i := 0; i < len(plain); i += block.BlockSize() {
			block.Encrypt(out[i:i+block.BlockSize()], plain[i:i+block.BlockSize()])
		}
		return out, nil
	default: // cbc
		ivp := padKey(iv, block.BlockSize())
		out := make([]byte, len(plain))
		cipher.NewCBCEncrypter(block, ivp).CryptBlocks(out, plain)
		return out, nil
	}
}

// padKey extends or truncates a key/iv to size, as the cipher requires.
func padKey(b []byte, size int) []byte {
	if len(b) == size {
		return b
	}
	out := make([]byte, size)
	copy(out, b)
	return out
}

// pkcs7 pads data to a multiple of size.
func pkcs7(data []byte, size int) []byte {
	pad := size - len(data)%size
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}
